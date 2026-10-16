// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"context"
	"fmt"
	"sort"
	"strings"
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

// adcHealth is the converter checks' state (owner goroutine only).
type adcHealth struct {
	// snapshot reads the ten cores at one instant; restart restarts their
	// encode clocks (the SRAM fabric's, or a test's fake).
	snapshot func(context.Context) ([10]uint8, error)
	restart  func() error
	next     time.Time // the next periodic check
	frame    time.Time // the next look at a full-rate record
	retry    time.Time // a failed recovery is tried again from then
	fault    uint16    // the cores left stuck (stuckCores' bits)
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
	vals := make([]int, n)
	for ch := 0; ch < 2; ch++ {
		var held [5]bool
		moving := false
		for p := 0; p < 5; p++ {
			for i, s := range snaps {
				vals[i] = int(s[2*p+ch])
			}
			first, same := vals[0], true
			for _, v := range vals {
				if v != first {
					same = false
					break
				}
			}
			held[p] = same
			if same {
				continue
			}
			sort.Ints(vals)
			med, away := vals[n/2], 0
			for _, v := range vals {
				if v-med >= adcMoving || med-v >= adcMoving {
					away++
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
	snaps := make([][10]uint8, m)
	for j := range snaps {
		k := 5 * (j * periods / m)
		for r := 0; r < 5; r++ {
			snaps[j][2*r], snaps[j][2*r+1] = c1[k+r], c2[k+r]
		}
	}
	return stuckCores(snaps)
}

// noteRecordCores looks at a full-rate record's raw codes at most once per
// adcFrameEvery; cores held in it bring the snapshot check forward
// (checkCoresDue), which names them and recovers them.
// TRLC-LINKS: REQ-SDS-231
func (e *Engine) noteRecordCores(c1, c2 []uint8) {
	now := e.clk.Now()
	if now.Before(e.adc.frame) {
		return
	}
	e.adc.frame = now.Add(adcFrameEvery)
	if recordStuck(c1, c2) != 0 && (e.adc.fault == 0 || !now.Before(e.adc.retry)) {
		e.adc.next = time.Time{}
	}
}

// coreNames lists stuck cores by converter, "E2" for both its cores and
// "E4 CH1" for one ("E2, E4 CH1").
// TRLC-LINKS: REQ-SDS-231
func coreNames(stuck uint16) string {
	var names []string
	for p := 0; p < 5; p++ {
		switch (stuck >> (2 * p)) & 3 {
		case 3:
			names = append(names, fmt.Sprintf("E%d", p+1))
		case 1, 2:
			names = append(names, fmt.Sprintf("E%d CH%d", p+1, 1+int(stuck>>(2*p+1))&1))
		}
	}
	return strings.Join(names, ", ")
}

// checkCores reads one check's snapshots and returns the stuck cores and
// what the check took.
// TRLC-LINKS: REQ-SDS-231
func (e *Engine) checkCores() (uint16, time.Duration, error) {
	snapshot := e.adc.snapshot
	if snapshot == nil {
		if e.sram == nil {
			return 0, 0, nil
		}
		snapshot = e.sram.Snapshot
	}
	start := time.Now()
	snaps := make([][10]uint8, adcSnaps)
	for i := range snaps {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		s, err := snapshot(ctx)
		cancel()
		if err != nil {
			return 0, time.Since(start), err
		}
		snaps[i] = s
	}
	took := time.Since(start)
	e.mu.Lock()
	e.stats.ADCCheckMs = float64(took.Microseconds()) / 1000
	e.mu.Unlock()
	return stuckCores(snaps), took, nil
}

// restartEncode restarts every converter's encode clock.
// TRLC-LINKS: REQ-SDS-231
func (e *Engine) restartEncode() error {
	if e.adc.restart != nil {
		return e.adc.restart()
	}
	if e.sram == nil {
		return nil
	}
	return e.sram.RestartEncode()
}

// startConverters is the converters' bring-up on a freshly configured
// fabric (the start and every image load alike): a clean restart of the
// encode clocks the configuration started, then a check (ensureCores) with
// the whole recovery, whenever the last one was.
// TRLC-LINKS: REQ-SDS-231
func (e *Engine) startConverters(why string) {
	e.adc.retry = time.Time{}
	if err := e.restartEncode(); err != nil {
		e.logf("engine: encode restart: %v", err)
	}
	e.ensureCores(why)
}

// ensureCores checks the converters after why (an image load, the start, a
// periodic check) and brings stuck cores back: an encode restart first, then
// up to adcReloads loads of the image in use, each checked again. Cores
// still stuck set Stats.ADCFault, which the screen shows, and are tried again
// after adcRetryAfter. Owner goroutine, between acquisitions.
// TRLC-LINKS: REQ-SDS-231
func (e *Engine) ensureCores(why string) {
	now := e.clk.Now()
	e.adc.next = now.Add(adcCheckEvery)
	stuck, _, err := e.checkCores()
	if err != nil {
		e.logf("engine: converter check: %v", err)
		return
	}
	if stuck == 0 {
		e.setADCFault(0)
		return
	}
	if e.adc.fault != 0 && now.Before(e.adc.retry) {
		e.setADCFault(stuck)
		return
	}
	e.logf("engine: converter cores stuck after %s: %s", why, coreNames(stuck))
	found := stuck
	for attempt := 0; attempt <= adcReloads; attempt++ {
		action := "an encode restart"
		if attempt == 0 {
			err = e.restartEncode()
		} else {
			action = "an image reload"
			if err = e.reloadImage(); err == nil {
				err = e.restartEncode() // the bring-up of startConverters
			}
		}
		if err != nil {
			e.logf("engine: converter recovery (%s): %v", action, err)
			continue
		}
		stuck, _, err = e.checkCores()
		if err != nil {
			e.logf("engine: converter check: %v", err)
			continue
		}
		if stuck == 0 {
			e.logf("engine: converter cores %s back after %s", coreNames(found), action)
			e.mu.Lock()
			e.stats.ADCRecoveries++
			e.stats.ADCRecovery = fmt.Sprintf("%s after %s: %s", action, why, coreNames(found))
			e.mu.Unlock()
			e.setADCFault(0)
			return
		}
	}
	e.logf("engine: converter cores still stuck after the recoveries: %s", coreNames(stuck))
	e.adc.retry = e.clk.Now().Add(adcRetryAfter)
	e.setADCFault(stuck)
}

// setADCFault publishes the cores left stuck (0: none).
// TRLC-LINKS: REQ-SDS-231
func (e *Engine) setADCFault(stuck uint16) {
	if stuck == e.adc.fault {
		return
	}
	if stuck == 0 {
		e.logf("engine: every converter core converts again")
	}
	e.adc.fault = stuck
	msg := ""
	if stuck != 0 {
		msg = "ADC fault: converter " + coreNames(stuck) + " not converting - restart the scope"
	}
	e.mu.Lock()
	e.stats.ADCFault = msg
	e.mu.Unlock()
}

// checkCoresDue runs the periodic check while acquiring.
// TRLC-LINKS: REQ-SDS-231
func (e *Engine) checkCoresDue() {
	if e.sram == nil && e.adc.snapshot == nil {
		return
	}
	if e.clk.Now().Before(e.adc.next) {
		return
	}
	e.ensureCores("a periodic check")
}
