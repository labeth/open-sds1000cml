// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"sync/atomic"
	"testing"
	"time"

	"open-sds/app/internal/decode"
	"open-sds/app/internal/engine"
)

// searchLeaseEng records the sample and decode leases a bus search takes.
type searchLeaseEng struct {
	*fakeEng
	sampled, raw atomic.Bool
	rawLeases    atomic.Int32
}

// TRLC-LINKS: REQ-SDS-214
func (e *searchLeaseEng) LeaseSampleView(d time.Duration) { e.sampled.Store(d > 0) }

// TRLC-LINKS: REQ-SDS-214
func (e *searchLeaseEng) LeaseDecodeView(d time.Duration) {
	e.raw.Store(d > 0)
	if d > 0 {
		e.rawLeases.Add(1)
	}
}

// A bus search looks at a decimated record first - read in tens of
// milliseconds - and keeps the bus found there when its bits span enough of
// that record's samples: a 115200 Bd UART at 200us/div (35 samples a bit)
// needs no full-rate record, and the sample lease ends with the search. A bus
// too fast for it (921600 Bd: 4.4 samples a bit) is looked at again at the
// full rate. Each lease ends with its phase.
// TRLC-LINKS: REQ-SDS-214
func TestBusSearchTakesADecimatedRecordFirst(t *testing.T) {
	for _, c := range []struct {
		baud    float64
		fullRun bool
	}{{115200, false}, {921600, true}} {
		_, base, fe := newC(t)
		eng := &searchLeaseEng{fakeEng: base}
		ctl := New(eng, fe, -1, engine.SupportedTdivs(), 200e-6, t.Logf)
		ctl.decode(idle(), true)
		ctl.detectSync = true
		msgs := []string{"Hi U\x55\xaa\x0f\xf0"}
		raw := uartFrame(msgs, c.baud, 30e-6, 2e-9, 1000000)
		dec := uartFrame(msgs, c.baud, 30e-6, 244e-9, 8192)
		env := envelope2(raw, 244)
		ctl.SetFrameSource(func(fn func(*engine.Frame)) {
			f := *env
			switch {
			case eng.raw.Load():
				f = *raw
			case eng.sampled.Load():
				f = *dec
			}
			fn(&f)
		})
		auto := func() (decode.Result, float64, bool) {
			var res decode.Result
			var sampleS float64
			ctl.frameFn(func(f *engine.Frame) {
				if f.PeakDetect || f.IsEnv {
					return
				}
				res, sampleS = decode.AutodetectFast(f.C1[:f.Valid], f.C2[:f.Valid], f.SampleS, "hex")
			})
			return res, sampleS, res.OK && res.Proto == "uart"
		}
		res, sampleS, found, _ := ctl.findBus(auto, func() bool { return true })
		if !found || res.Proto != "uart" {
			t.Fatalf("%.0f Bd: found %v %q", c.baud, found, res.Proto)
		}
		if ran := eng.rawLeases.Load() > 0; ran != c.fullRun {
			t.Errorf("%.0f Bd: a full-rate record read %v, want %v (decoded at %.3g s a sample, %.1f samples a bit)",
				c.baud, ran, c.fullRun, sampleS, decode.FeatureSamples(res))
		}
		if eng.sampled.Load() || eng.raw.Load() {
			t.Errorf("%.0f Bd: a lease outlived the search (sample %v, full rate %v)", c.baud, eng.sampled.Load(), eng.raw.Load())
		}
	}
}
