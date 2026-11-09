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

	"github.com/labeth/open-sds1000cml/app/internal/board/acq"
)

// adcSnaps is the snapshots one of the board's converter checks reads.
const adcSnaps = 64

// adcReloads is the image reloads the board tries after an encode restart.
const adcReloads = 3

// snapsOf samples a signal at n random instants, as the fabric's snapshots
// do: core (p, ch) reads sig(t + its sampling phase) plus noise, held cores
// read their held code.
// TRLC-LINKS: REQ-SDS-231
func snapsOf(n int, seed int64, noise float64, sig func(t float64) float64, held map[int]uint8) [][10]uint8 {
	r := rand.New(rand.NewSource(seed))
	out := make([][10]uint8, n)
	for i := range out {
		t := r.Float64()
		for b := range 10 {
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
	t.Helper()
	e, b, _ := newPacketTestEngine(t)
	calls := &[]string{}
	conv.calls = calls
	images := &convImages{packetImages: packetImages{b: b}, conv: conv, calls: calls, breakOn: map[string]bool{"fast": true}}
	e.images = images
	e.hw().SetImages(images)
	e.hw().SetConverterPort(acq.ConverterPort{Snapshot: conv.snapshot, Restart: conv.restart})
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
	e.loadImage(acq.ImageFast)
	if got := strings.Join(*calls, ","); got != "load fast,encode,check" {
		t.Errorf("bring-up: %s", got)
	}
	if st := e.Snapshot(); st.ADCFault != "" || st.ADCRecoveries != 0 {
		t.Errorf("stats %q %d %q", st.ADCFault, st.ADCRecoveries, st.ADCRecovery)
	}

	// a second encode restart
	conv = &fakeConverters{encFrom: 2}
	e, _, calls = newConvEngine(t, conv)
	e.loadImage(acq.ImageFast)
	if got := strings.Join(*calls, ","); got != "load fast,encode,check,encode,check" {
		t.Errorf("encode restart: %s", got)
	}
	if st := e.Snapshot(); st.ADCFault != "" || st.ADCRecoveries != 1 || !strings.HasPrefix(st.ADCRecovery, "an encode restart after loading the fast image: E2") {
		t.Errorf("stats %q %d %q", st.ADCFault, st.ADCRecoveries, st.ADCRecovery)
	}

	// the encode restarts do not help: the second load of the image does
	conv = &fakeConverters{reloads: 1}
	e, _, calls = newConvEngine(t, conv)
	e.loadImage(acq.ImageFast)
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
	e.loadImage(acq.ImageFast)
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
	e.loadImage(acq.ImageFast)
	if got := strings.Join(*calls, ","); got != want {
		t.Errorf("another load: %s", got)
	}
	// periodic checks: not before they are due, and no recovery until the retry
	*calls = nil
	e.checkCoresDue()
	now = now.Add(time.Minute) // the board checks every minute
	e.checkCoresDue()
	if got := strings.Join(*calls, ","); got != "check" {
		t.Errorf("periodic: %s", got)
	}
	*calls = nil
	now = now.Add(5 * time.Minute) // the board retries a failed recovery after five
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
	e.hw().SetConverterPort(acq.ConverterPort{
		Snapshot: func(context.Context) ([10]uint8, error) { return [10]uint8{}, errors.New("bus") },
		Restart:  func() error { t.Error("restarted on a read error"); return nil },
	})
	e.ensureCores("a test")
	if st := e.Snapshot(); st.ADCFault != "" {
		t.Errorf("fault %q", st.ADCFault)
	}
}

// A check ends once every core has read two codes (none can be stuck), a few
// snapshots into a live input; a core that holds one code is still found,
// on all 64.
// TRLC-LINKS: REQ-SDS-231
func TestConverterCheckEndsWhenEveryCoreMoved(t *testing.T) {
	conv := &fakeConverters{}
	e, _, _ := newConvEngine(t, conv)
	stuck, _, err := e.hw().CheckCores()
	if err != nil || stuck != 0 {
		t.Fatalf("live cores: stuck %b err %v", stuck, err)
	}
	if conv.n >= adcSnaps/4 {
		t.Fatalf("live cores read %d snapshots, want a few", conv.n)
	}
	conv.n, conv.stuck = 0, map[int]uint8{2: 165}
	if stuck, _, err = e.hw().CheckCores(); err != nil || stuck != 1<<2 {
		t.Fatalf("a held core: stuck %b err %v", stuck, err)
	}
	if conv.n != adcSnaps {
		t.Fatalf("a held core read %d snapshots, want %d", conv.n, adcSnaps)
	}
}
