// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package acq

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Converter health. Now and then a configuration of the acquisition FPGA
// leaves converters that never convert: both cores of such a converter (one
// CH1, one CH2) hold one code while the channels' other cores follow the
// signal, and every capture shows a fake pattern at a fifth of the sample
// rate. The encode clocks start with the configuration, as its PLL locks;
// stopped and started again cleanly they bring the converters back, as
// another configuration does. So on every freshly configured fabric (the
// start, each image load) the engine restarts the encode clocks, then reads
// the cores; it reads them again every minute while acquiring and, when some
// hold still under a moving input, restarts the encode clocks, then reloads
// the image, and says so on screen if they stay stuck. A check
// reads 64 snapshots of the cores (about 130 ms of bus reads on the unit), so
// between them the full-rate records' own samples are looked at, for free,
// and a record with a held core brings the next check forward.

const (
	// adcSnaps is the snapshots one check reads: a held core is told from a
	// live one by 64 instants of a moving input.
	adcSnaps = 64
	// adcMoving is how far (codes) from a core's median a snapshot must be
	// to count as the input moving, beyond the converters' noise.
	adcMoving = 3
	// adcCheckEvery spaces the snapshot checks while acquiring.
	adcCheckEvery = time.Minute
	// adcFrameEvery spaces the looks at full-rate records.
	adcFrameEvery = time.Second
	// adcReloads is the image reloads tried after the encode restart.
	adcReloads = 3
	// adcRetryAfter spaces the recoveries of cores that stayed stuck.
	adcRetryAfter = 5 * time.Minute
)

// ConverterPort reads the ten converter cores at one instant and restarts
// their encode clocks: the SRAM fabric's, or a test's.
type ConverterPort struct {
	Snapshot func(context.Context) ([10]uint8, error)
	Restart  func() error
}

// ConverterHealth is what the converter checks found, for the status.
type ConverterHealth struct {
	// Fault names the converters that do not convert, as the screen says it
	// ("" while every core converts).
	Fault string
	// Recoveries counts stuck cores brought back, Recovery says how the last
	// came back.
	Recoveries int64
	Recovery   string
	// CheckMs is the last check's duration.
	CheckMs float64
}

// adcHealth is the converter checks' state; the schedule is the owner's,
// the published health is read by any goroutine (mu).
type adcHealth struct {
	port  ConverterPort
	next  time.Time // the next periodic check
	frame time.Time // the next look at a full-rate record
	retry time.Time // a failed recovery is tried again from then
	fault uint16    // the cores left stuck (stuckCores' bits)

	mu        sync.Mutex
	published ConverterHealth
}

// SetConverterPort replaces the fabric's snapshot and encode restart (a
// test's converters); a nil function keeps the fabric's.
// TRLC-LINKS: REQ-SDS-231
func (b *Board) SetConverterPort(p ConverterPort) { b.adc.port = p }

// ConverterHealth is the converter checks' findings. ANY.
// TRLC-LINKS: REQ-SDS-231
func (b *Board) ConverterHealth() ConverterHealth {
	b.adc.mu.Lock()
	defer b.adc.mu.Unlock()
	return b.adc.published
}

// ConverterCheckDue is when the next periodic converter check falls (zero:
// at the next commit point). OWNER.
// TRLC-LINKS: REQ-SDS-231
func (b *Board) ConverterCheckDue() time.Time { return b.adc.next }

// SetConverterCheckDue moves the next periodic converter check. OWNER.
// TRLC-LINKS: REQ-SDS-231
func (b *Board) SetConverterCheckDue(t time.Time) { b.adc.next = t }

// TRLC-LINKS: REQ-SDS-231
func (b *Board) publishHealth(f func(*ConverterHealth)) {
	b.adc.mu.Lock()
	f(&b.adc.published)
	b.adc.mu.Unlock()
}

// stuckCores finds the cores that hold one code while another core of their
// channel follows a moving input. Snapshot byte 2p is converter E(p+1)'s CH1
// core, 2p+1 its CH2 core (adc_unpack's order); bit 2p+ch of the result is
// that core. A quiet, slow or clipped input moves no core and finds nothing:
// a held code is evidence only against a core that should have moved with
// its neighbours.
// TRLC-LINKS: REQ-SDS-231
func stuckCores(snaps [][10]uint8) uint16 {
	n := len(snaps)
	if n < adcSnaps/2 {
		return 0
	}
	var stuck uint16
	for ch := range 2 {
		var held [5]bool
		moving := false
		for p := range 5 {
			// the core's codes as a histogram: held is all of them one code,
			// the median the code whose running count passes n/2 (what a sort
			// put at n/2)
			var hist [256]int
			for _, s := range snaps {
				hist[s[2*p+ch]]++
			}
			same := hist[snaps[0][2*p+ch]] == n
			held[p] = same
			if same {
				continue
			}
			med, cum := 0, 0
			for v, k := range hist {
				if cum += k; cum > n/2 {
					med = v
					break
				}
			}
			away := 0
			for v, k := range hist {
				if v-med >= adcMoving || med-v >= adcMoving {
					away += k
				}
			}
			if away >= n/8 {
				moving = true
			}
		}
		if !moving {
			continue
		}
		for p, h := range held {
			if h {
				stuck |= 1 << (2*p + ch)
			}
		}
	}
	return stuck
}

// recordStuck looks at a full-rate record's raw codes (2 ns apart, so
// sample i of a channel is core i mod 5 of it) as stuckCores looks at
// snapshots: up to 512 instants spread over the record. Its bits number the
// cores by their place in the record, not by converter.
// TRLC-LINKS: REQ-SDS-231
func recordStuck(c1, c2 []uint8) uint16 {
	periods := min(len(c1), len(c2)) / 5
	if periods < adcSnaps {
		return 0
	}
	m := min(periods, 512)
	var buf [512][10]uint8 // the snapshots, on the stack
	snaps := buf[:m]
	at, rem := 0, 0 // j·periods/m, stepped: a period index and its remainder
	step, frac := periods/m, periods%m
	for j := range snaps {
		k := 5 * at
		for r := range 5 {
			snaps[j][2*r], snaps[j][2*r+1] = c1[k+r], c2[k+r]
		}
		at += step
		if rem += frac; rem >= m {
			rem -= m
			at++
		}
	}
	return stuckCores(snaps)
}

// NoteRecordCores looks at a full-rate record's raw codes at most once per
// adcFrameEvery; cores held in it bring the snapshot check forward
// (CheckCoresDue), which names them and recovers them. OWNER.
// TRLC-LINKS: REQ-SDS-231
func (b *Board) NoteRecordCores(c1, c2 []uint8) {
	now := b.now()
	if now.Before(b.adc.frame) {
		return
	}
	b.adc.frame = now.Add(adcFrameEvery)
	if recordStuck(c1, c2) != 0 && (b.adc.fault == 0 || !now.Before(b.adc.retry)) {
		b.adc.next = time.Time{}
	}
}

// coreNames lists stuck cores by converter, "E2" for both its cores and
// "E4 CH1" for one ("E2, E4 CH1").
// TRLC-LINKS: REQ-SDS-231
func coreNames(stuck uint16) string {
	var names []string
	for p := range 5 {
		switch (stuck >> (2 * p)) & 3 {
		case 3:
			names = append(names, fmt.Sprintf("E%d", p+1))
		case 1, 2:
			names = append(names, fmt.Sprintf("E%d CH%d", p+1, 1+int(stuck>>(2*p+1))&1))
		}
	}
	return strings.Join(names, ", ")
}

// CheckCores reads one check's snapshots and returns the stuck cores and
// what the check took. OWNER.
// TRLC-LINKS: REQ-SDS-231
func (b *Board) CheckCores() (uint16, time.Duration, error) {
	snapshot := b.adc.port.Snapshot
	if snapshot == nil {
		if b.mem == nil {
			return 0, 0, nil
		}
		snapshot = b.mem.Snapshot
	}
	start := time.Now()
	snaps := make([][10]uint8, adcSnaps)
	// Only a core that read one code in every snapshot can be stuck: once
	// every core has read two, none is, and the rest of the check cannot
	// change that (a live input ends it after a few snapshots, not 64).
	var differs uint16
	stuck := uint16(0)
	for i := range snaps {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		s, err := snapshot(ctx)
		cancel()
		if err != nil {
			return 0, time.Since(start), err
		}
		snaps[i] = s
		for k := range s {
			if s[k] != snaps[0][k] {
				differs |= 1 << k
			}
		}
		if differs == 1<<10-1 {
			snaps = snaps[:i+1]
			break
		}
	}
	if len(snaps) == adcSnaps {
		stuck = stuckCores(snaps)
	}
	took := time.Since(start)
	b.publishHealth(func(h *ConverterHealth) { h.CheckMs = float64(took.Microseconds()) / 1000 })
	return stuck, took, nil
}

// restartEncode restarts every converter's encode clock.
// TRLC-LINKS: REQ-SDS-231
func (b *Board) restartEncode() error {
	if b.adc.port.Restart != nil {
		return b.adc.port.Restart()
	}
	if b.mem == nil {
		return nil
	}
	return b.mem.RestartEncode()
}

// errNoReload is a recovery's reload where none was given.
var errNoReload = errors.New("no image switcher")

// StartConverters is the converters' bring-up on a freshly configured
// fabric (the start and every image load alike): a clean restart of the
// encode clocks the configuration started, then a check (EnsureCores) with
// the whole recovery, whenever the last one was. reload configures the
// image in use again. OWNER.
// TRLC-LINKS: REQ-SDS-231
func (b *Board) StartConverters(why string, reload func() error) {
	b.adc.retry = time.Time{}
	if err := b.restartEncode(); err != nil {
		b.logf("engine: encode restart: %v", err)
	}
	b.beat()
	b.EnsureCores(why, reload)
	b.beat()
}

// EnsureCores checks the converters after why (an image load, the start, a
// periodic check) and brings stuck cores back: an encode restart first, then
// up to adcReloads reloads (reload: the image in use configured again, nil:
// none), each checked again. Cores still stuck set the health's Fault,
// which the screen shows, and are tried again after adcRetryAfter. OWNER,
// between acquisitions.
// TRLC-LINKS: REQ-SDS-231
func (b *Board) EnsureCores(why string, reload func() error) {
	now := b.now()
	b.adc.next = now.Add(adcCheckEvery)
	stuck, _, err := b.CheckCores()
	if err != nil {
		b.logf("engine: converter check: %v", err)
		return
	}
	if stuck == 0 {
		b.setADCFault(0)
		return
	}
	if b.adc.fault != 0 && now.Before(b.adc.retry) {
		b.setADCFault(stuck)
		return
	}
	b.logf("engine: converter cores stuck after %s: %s", why, coreNames(stuck))
	found := stuck
	for attempt := 0; attempt <= adcReloads; attempt++ {
		b.beat() // each recovery is a step of LONG work
		action := "an encode restart"
		if attempt == 0 {
			err = b.restartEncode()
		} else {
			action = "an image reload"
			if reload == nil {
				err = errNoReload
			} else if err = reload(); err == nil {
				err = b.restartEncode() // the bring-up of StartConverters
			}
		}
		if err != nil {
			b.logf("engine: converter recovery (%s): %v", action, err)
			continue
		}
		stuck, _, err = b.CheckCores()
		if err != nil {
			b.logf("engine: converter check: %v", err)
			continue
		}
		if stuck == 0 {
			b.logf("engine: converter cores %s back after %s", coreNames(found), action)
			b.publishHealth(func(h *ConverterHealth) {
				h.Recoveries++
				h.Recovery = fmt.Sprintf("%s after %s: %s", action, why, coreNames(found))
			})
			b.setADCFault(0)
			return
		}
	}
	b.logf("engine: converter cores still stuck after the recoveries: %s", coreNames(stuck))
	b.adc.retry = b.now().Add(adcRetryAfter)
	b.setADCFault(stuck)
}

// setADCFault publishes the cores left stuck (0: none).
// TRLC-LINKS: REQ-SDS-231
func (b *Board) setADCFault(stuck uint16) {
	if stuck == b.adc.fault {
		return
	}
	if stuck == 0 {
		b.logf("engine: every converter core converts again")
	}
	b.adc.fault = stuck
	msg := ""
	if stuck != 0 {
		msg = "ADC fault: converter " + coreNames(stuck) + " not converting - restart the scope"
	}
	b.publishHealth(func(h *ConverterHealth) { h.Fault = msg })
}

// CheckCoresDue runs the periodic check while acquiring, when it is due,
// and reports whether it ran. OWNER.
// TRLC-LINKS: REQ-SDS-231
func (b *Board) CheckCoresDue(reload func() error) bool {
	if b.mem == nil && b.adc.port.Snapshot == nil {
		return false
	}
	if b.now().Before(b.adc.next) {
		return false
	}
	b.EnsureCores("a periodic check", reload)
	return true
}
