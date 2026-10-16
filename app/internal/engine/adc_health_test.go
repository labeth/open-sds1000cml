// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"context"
	"errors"
	"math"
	"math/rand"
	"strings"
	"testing"
	"time"
)

// snapsOf samples a signal at n random instants, as the fabric's snapshots
// do: core (p, ch) reads sig(t + its sampling phase) plus noise, held cores
// read their held code.
// TRLC-LINKS: REQ-SDS-231
func snapsOf(n int, seed int64, noise float64, sig func(t float64) float64, held map[int]uint8) [][10]uint8 {
	r := rand.New(rand.NewSource(seed))
	out := make([][10]uint8, n)
	for i := range out {
		t := r.Float64()
		for b := 0; b < 10; b++ {
			if v, ok := held[b]; ok {
				out[i][b] = v
				continue
			}
			v := sig(t+float64(b)*1e-9) + r.NormFloat64()*noise
			out[i][b] = uint8(math.Max(0, math.Min(255, math.Round(v))))
		}
	}
	return out
}

// TRLC-LINKS: REQ-SDS-231
func adcSquare(t float64) float64 {
	if math.Mod(t*1e4, 1) < .5 {
		return 90
	}
	return 160
}

// A core holding one code under a moving input is stuck; a quiet, flat,
// clipped or slow input moves no core and finds none.
// TRLC-LINKS: REQ-SDS-231
func TestStuckCores(t *testing.T) {
	sine := func(t float64) float64 { return 128 + 60*math.Sin(2*math.Pi*1e4*t) }
	for _, c := range []struct {
		name  string
		snaps [][10]uint8
		want  uint16
	}{
		{"healthy adcSquare", snapsOf(adcSnaps, 1, .6, adcSquare, nil), 0},
		{"healthy sine", snapsOf(adcSnaps, 2, .6, sine, nil), 0},
		// converters E2 and E4: both cores each (bits 2,3 and 6,7)
		{"stuck pairs", snapsOf(adcSnaps, 3, .6, adcSquare, map[int]uint8{2: 165, 3: 177, 6: 181, 7: 120}), 0b11001100},
		// one core of a channel whose other cores move
		{"stuck core", snapsOf(adcSnaps, 4, .6, sine, map[int]uint8{9: 97}), 1 << 9},
		// four of a channel's five cores held: the one moving is evidence
		{"one core moving", snapsOf(adcSnaps, 5, .6, adcSquare, map[int]uint8{0: 85, 2: 90, 4: 91, 6: 97}), 0b1010101},
		{"quiet input", snapsOf(adcSnaps, 6, .7, func(float64) float64 { return 128 }, nil), 0},
		{"quiet input, one held", snapsOf(adcSnaps, 7, .7, func(float64) float64 { return 128 }, map[int]uint8{4: 128}), 0},
		{"noise-free flat", snapsOf(adcSnaps, 8, 0, func(float64) float64 { return 131 }, nil), 0},
		{"clipped high", snapsOf(adcSnaps, 9, .6, func(float64) float64 { return 400 }, nil), 0},
		{"clipped low, one held", snapsOf(adcSnaps, 10, .6, func(float64) float64 { return -50 }, map[int]uint8{1: 90}), 0},
		// a sine clipped at both rails still moves every live core
		{"clipped sine", snapsOf(adcSnaps, 11, .6, func(t float64) float64 { return 128 + 400*math.Sin(2*math.Pi*1e4*t) }, map[int]uint8{5: 255}), 1 << 5},
		// a slow input: every instant reads the same level
		{"slow", snapsOf(adcSnaps, 12, 0, func(t float64) float64 { return 100 + t }, nil), 0},
		{"too few snapshots", snapsOf(adcSnaps/4, 13, .6, adcSquare, map[int]uint8{2: 165}), 0},
	} {
		if got := stuckCores(c.snaps); got != c.want {
			t.Errorf("%s: stuck %010b, want %010b", c.name, got, c.want)
		}
	}
	if got := coreNames(0b11001100 | 1<<9); got != "E2, E4, E5 CH2" {
		t.Errorf("names %q", got)
	}
}

// fakeConverters is the hardware behind a converter check: the cores a
// configuration left stuck, cleared by what the test says clears them.
type fakeConverters struct {
	stuck    map[int]uint8
	calls    *[]string
	n        int
	encFrom  int // the encode restart since they stuck that brings them back (0: none)
	restarts int
	reloads  int // the reloads it takes (0: none does)
}

// TRLC-LINKS: REQ-SDS-231
func (f *fakeConverters) snapshot(context.Context) ([10]uint8, error) {
	if f.n%adcSnaps == 0 {
		*f.calls = append(*f.calls, "check")
	}
	f.n++
	s := snapsOf(1, int64(f.n), .6, adcSquare, f.stuck)
	return s[0], nil
}

// TRLC-LINKS: REQ-SDS-231
func (f *fakeConverters) restart() error {
	*f.calls = append(*f.calls, "encode")
	f.restarts++
	if f.encFrom > 0 && f.restarts >= f.encFrom {
		f.stuck = nil
	}
	return nil
}

// convImages is an image switcher whose every load may leave cores stuck.
type convImages struct {
	packetImages
	conv    *fakeConverters
	calls   *[]string
	breakOn map[string]bool // the image whose load leaves E2 stuck
	loaded  int
}

// TRLC-LINKS: REQ-SDS-231
func (f *convImages) load(name string) error {
	*f.calls = append(*f.calls, "load "+name)
	f.loaded++
	if f.breakOn[name] && f.loaded == 1 {
		f.conv.stuck, f.conv.restarts = map[int]uint8{2: 165, 3: 177}, 0
	} else if f.conv.reloads > 0 && f.loaded > f.conv.reloads {
		f.conv.stuck = nil
	}
	return nil
}

// TRLC-LINKS: REQ-SDS-231
func (f *convImages) LoadFast() error { return f.load("fast") }

// TRLC-LINKS: REQ-SDS-231
func (f *convImages) LoadGeneral() error { return f.load("general") }

// TRLC-LINKS: REQ-SDS-231
func newConvEngine(t *testing.T, conv *fakeConverters) (*Engine, *convImages, *[]string) {
	e, b, _ := newPacketTestEngine(t)
	calls := &[]string{}
	conv.calls = calls
	images := &convImages{packetImages: packetImages{b: b}, conv: conv, calls: calls, breakOn: map[string]bool{"fast": true}}
	e.images = images
	e.adc.snapshot, e.adc.restart = conv.snapshot, conv.restart
	return e, images, calls
}

// Every image load brings the converters up as the start does, an encode
// restart and a check, and stuck cores come back by another encode restart,
// else by reloading the image, in that order.
// TRLC-LINKS: REQ-SDS-231
func TestImageLoadChecksConverters(t *testing.T) {
	// the bring-up's encode restart is the cure: nothing to recover
	conv := &fakeConverters{encFrom: 1}
	e, _, calls := newConvEngine(t, conv)
	e.loadImage(imageFast)
	if got := strings.Join(*calls, ","); got != "load fast,encode,check" {
		t.Errorf("bring-up: %s", got)
	}
	if st := e.Snapshot(); st.ADCFault != "" || st.ADCRecoveries != 0 {
		t.Errorf("stats %q %d %q", st.ADCFault, st.ADCRecoveries, st.ADCRecovery)
	}

	// a second encode restart
	conv = &fakeConverters{encFrom: 2}
	e, _, calls = newConvEngine(t, conv)
	e.loadImage(imageFast)
	if got := strings.Join(*calls, ","); got != "load fast,encode,check,encode,check" {
		t.Errorf("encode restart: %s", got)
	}
	if st := e.Snapshot(); st.ADCFault != "" || st.ADCRecoveries != 1 || !strings.HasPrefix(st.ADCRecovery, "an encode restart after loading the fast image: E2") {
		t.Errorf("stats %q %d %q", st.ADCFault, st.ADCRecoveries, st.ADCRecovery)
	}

	// the encode restarts do not help: the second load of the image does
	conv = &fakeConverters{reloads: 1}
	e, _, calls = newConvEngine(t, conv)
	e.loadImage(imageFast)
	if got := strings.Join(*calls, ","); got != "load fast,encode,check,encode,check,load fast,encode,check" {
		t.Errorf("reload: %s", got)
	}
	if st := e.Snapshot(); st.ADCFault != "" || st.ADCRecoveries != 1 || !strings.HasPrefix(st.ADCRecovery, "an image reload after") {
		t.Errorf("stats %q %d %q", st.ADCFault, st.ADCRecoveries, st.ADCRecovery)
	}

	// a healthy load: one check, nothing else
	conv = &fakeConverters{}
	e, images, calls := newConvEngine(t, conv)
	images.breakOn = nil
	e.loadImage("")
	if got := strings.Join(*calls, ","); got != "load general,encode,check" {
		t.Errorf("healthy: %s", got)
	}
}

// Cores that stay stuck say so on the status (the screen's notice), are
// tried again after a while, not on every check, and the notice goes once
// they convert.
// TRLC-LINKS: REQ-SDS-231
func TestStuckConvertersAreReported(t *testing.T) {
	conv := &fakeConverters{}
	e, _, calls := newConvEngine(t, conv)
	now := time.Unix(1000, 0)
	e.clk.Now = func() time.Time { return now }
	e.loadImage(imageFast)
	want := "load fast,encode,check,encode,check" + strings.Repeat(",load fast,encode,check", adcReloads)
	if got := strings.Join(*calls, ","); got != want {
		t.Errorf("recoveries: %s", got)
	}
	st := e.Snapshot()
	if st.ADCFault != "ADC fault: converter E2 not converting - restart the scope" || st.ADCRecoveries != 0 {
		t.Errorf("fault %q recoveries %d", st.ADCFault, st.ADCRecoveries)
	}
	// a fresh configuration runs the whole recovery again
	*calls = nil
	e.loadImage(imageFast)
	if got := strings.Join(*calls, ","); got != want {
		t.Errorf("another load: %s", got)
	}
	// periodic checks: not before they are due, and no recovery until the retry
	*calls = nil
	e.checkCoresDue()
	now = now.Add(adcCheckEvery)
	e.checkCoresDue()
	if got := strings.Join(*calls, ","); got != "check" {
		t.Errorf("periodic: %s", got)
	}
	*calls = nil
	now = now.Add(adcRetryAfter)
	conv.encFrom = conv.restarts + 1
	e.checkCoresDue()
	if got := strings.Join(*calls, ","); got != "check,encode,check" {
		t.Errorf("retry: %s", got)
	}
	if st := e.Snapshot(); st.ADCFault != "" || st.ADCRecoveries != 1 {
		t.Errorf("after the retry: fault %q recoveries %d", st.ADCFault, st.ADCRecoveries)
	}
}

// A check that cannot read the cores changes nothing.
// TRLC-LINKS: REQ-SDS-231
func TestConverterCheckReadError(t *testing.T) {
	e, _, _ := newPacketTestEngine(t)
	e.adc.snapshot = func(context.Context) ([10]uint8, error) { return [10]uint8{}, errors.New("bus") }
	e.adc.restart = func() error { t.Error("restarted on a read error"); return nil }
	e.ensureCores("a test")
	if st := e.Snapshot(); st.ADCFault != "" {
		t.Errorf("fault %q", st.ADCFault)
	}
}

// A full-rate record shows a held core as every fifth sample at one code;
// one brings the snapshot check forward, at most one look a second.
// TRLC-LINKS: REQ-SDS-231
func TestRecordStuckBringsCheckForward(t *testing.T) {
	rec := func(held int, quiet bool) ([]uint8, []uint8) {
		r := rand.New(rand.NewSource(int64(held) + 7))
		c1, c2 := make([]uint8, 5000), make([]uint8, 5000)
		for i := range c1 {
			v := 128 + 50*math.Sin(2*math.Pi*float64(i)/37)
			if quiet {
				v = 128
			}
			c1[i] = uint8(v + r.NormFloat64())
			c2[i] = uint8(255 - v + r.NormFloat64())
			if held >= 0 && i%5 == held {
				c1[i] = 93
			}
		}
		return c1, c2
	}
	if s := recordStuck(rec(-1, false)); s != 0 {
		t.Errorf("healthy record: %010b", s)
	}
	if s := recordStuck(rec(3, false)); s != 1<<6 {
		t.Errorf("held core 3 of CH1: %010b", s)
	}
	if s := recordStuck(rec(3, true)); s != 0 {
		t.Errorf("quiet record: %010b", s)
	}
	if s := recordStuck(make([]uint8, 100), make([]uint8, 100)); s != 0 {
		t.Errorf("short record: %010b", s)
	}
	e, _, _ := newPacketTestEngine(t)
	now := time.Unix(1000, 0)
	e.clk.Now = func() time.Time { return now }
	e.adc.next = now.Add(adcCheckEvery)
	e.noteRecordCores(rec(-1, false))
	if !e.adc.next.After(now) {
		t.Error("a healthy record brought the check forward")
	}
	e.noteRecordCores(rec(2, false))
	if !e.adc.next.After(now) {
		t.Error("looked again within a second")
	}
	now = now.Add(adcFrameEvery)
	e.noteRecordCores(rec(2, false))
	if e.adc.next.After(now) {
		t.Error("a held core did not bring the check forward")
	}
}
