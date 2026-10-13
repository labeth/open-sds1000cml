// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"math"
	"testing"
	"time"

	"open-sds/app/internal/sramcapture"
)

// A view is read only where the screen's envelope is coarser than the display
// draws it: a bucket every second column or fewer.
// TRLC-LINKS: REQ-SDS-021
func TestViewBuckets(t *testing.T) {
	for _, c := range []struct {
		name         string
		a, b         float64
		cols         int
		screen, want uint32
	}{
		{"LCD zoom 2: 1024 buckets over 770 columns", -0.25, 0.25, 770, 2048, 0},
		{"LCD zoom 5: 410 buckets, about one every second column", -0.1, 0.1, 770, 2048, 0},
		{"LCD zoom 10", -0.05, 0.05, 770, 2048, 770},
		{"LCD zoom 50, odd columns", -0.01, 0.01, 771, 2048, 772},
		{"canvas zoom 2", -0.25, 0.25, 1900, 2048, 0},
		{"canvas zoom 5: capped", -0.1, 0.1, 1900, 2048, viewMaxBuckets},
		{"Q8 screen, canvas zoom 2", -0.25, 0.25, 1900, 1024, viewMaxBuckets},
		{"empty", 0.1, 0.1, 770, 2048, 0},
		{"no columns", -0.05, 0.05, 1, 2048, 0},
	} {
		if got := viewBuckets(c.a, c.b, c.cols, c.screen); got != c.want {
			t.Errorf("%s: %d buckets, want %d", c.name, got, c.want)
		}
	}
}

// The scope's zoom asks for its stretch in screen widths from the trigger; the
// view read for it lies among the frame's values where the LCD draws the zoom,
// covering it, triggered or not, panned or not.
// TRLC-LINKS: REQ-SDS-021
func TestViewCoversTheZoom(t *testing.T) {
	const screen = 1000000 // 200us/div at 2 ns: samples across the screen
	checked := 0
	for _, trig := range []uint32{262144, 40000, 500000} {
		m := sramcapture.Metadata{Length: sramcapture.Words, SamplesPerWord: 2, TriggerIndex: trig, SampleRateHz: 500e6}
		start := min(max(int(trig)-screen/4, 0), int(m.Length)-screen/2) // the screen's words, within the record
		bucket, offset, words := envelopeWindow(uint32(start), screen/2, sramcapture.EnvelopeBuckets, m.Length)
		half := float64(bucket) * float64(m.SamplesPerWord) / 2
		valid := 2 * int(sramcapture.EnvelopeBuckets)
		winCols := min(valid, int(screen/half+0.5))
		for _, edgeX := range []float64{float64(trig-offset) * 2 / float64(bucket), -1} {
			for _, zoom := range []int{10, 20, 50} {
				for _, zoomOff := range []float64{0, 0.1, -0.2} {
					// the request main.go makes, and the window the LCD draws (traceWindow)
					c := zoomOff * float64(valid) / float64(winCols)
					a, b := c-0.5/float64(zoom), c+0.5/float64(zoom)
					xc := edgeX
					if xc < 0 {
						xc = float64(valid) / 2
					}
					win := float64(winCols / zoom)
					centre := xc + zoomOff*float64(valid)
					za, zb := centre-win/2, centre+win/2
					n := viewBuckets(a, b, 770, sramcapture.EnvelopeBuckets)
					if n == 0 {
						t.Fatalf("zoom %d: no view", zoom)
					}
					from, to := viewSpan(m, screen, offset, words, edgeX, a, b)
					p, ok := planView(m, from, to, n)
					if !ok {
						if to <= 0 || from >= float64(m.Length) {
							continue // the zoom is off the record
						}
						t.Fatalf("trig %d zoom %d off %.1f: no plan for words %.0f..%.0f", trig, zoom, zoomOff, from, to)
					}
					at := envelopeValue(offset, bucket)
					vFrom, vTo := at(p.start), at(p.start+p.length)
					if za < at(0) || zb > at(m.Length) {
						continue // the zoom runs off the record: the view is clipped to it
					}
					if tol := win / 100; vFrom > za+tol || vTo < zb-tol {
						t.Errorf("trig %d edge %.0f zoom %d off %.1f: view %.1f..%.1f does not cover the zoom %.1f..%.1f",
							trig, edgeX, zoom, zoomOff, vFrom, vTo, za, zb)
					}
					// centred on the zoom (widened about it), to two of its values
					if clipped := p.start == 0 || p.start+p.length >= m.Length; !clipped {
						if d, two := math.Abs((vFrom+vTo)/2-centre), 2*(vTo-vFrom)/float64(p.n); d > two {
							t.Errorf("trig %d edge %.0f zoom %d off %.1f: view centred %.2f values from the zoom's centre (allowed %.2f)",
								trig, edgeX, zoom, zoomOff, d, two)
						}
					}
					checked++
					if w := vTo - vFrom; w > 1.25*(zb-za)+4 {
						t.Errorf("zoom %d: view %.1f values wide for a zoom of %.1f", zoom, w, zb-za)
					}
					if want := 2 * int(n); p.bucket != 0 && p.n != want {
						t.Errorf("zoom %d: %d values, want %d", zoom, p.n, want)
					}
				}
			}
		}
	}
	if checked < 40 {
		t.Fatalf("only %d of 54 zooms checked", checked)
	}
}

// A view short enough is read raw: every sample, no envelope.
// TRLC-LINKS: REQ-SDS-021
func TestPlanViewRawWhenShort(t *testing.T) {
	m := sramcapture.Metadata{Length: sramcapture.Words, SamplesPerWord: 2, TriggerIndex: 1000}
	p, ok := planView(m, 900, 1100, 770)
	if !ok || p.bucket != 0 || p.start != 900 || p.length != 200 || p.n != 400 {
		t.Fatalf("plan %+v ok %v, want raw words 900..1100, 400 samples", p, ok)
	}
	if _, ok := planView(m, -50, 1, 770); ok {
		t.Fatal("a stretch before the record planned a read")
	}
	if _, ok := planView(m, math.NaN(), 10, 770); ok {
		t.Fatal("NaN planned a read")
	}
}

// Each surface leases its own view; an empty or non-finite request ends it.
// TRLC-LINKS: REQ-SDS-021
func TestLeaseViewWindowPerSurface(t *testing.T) {
	now := time.Unix(1000, 0)
	e := &Engine{}
	e.clk.Now = func() time.Time { return now }
	e.LeaseViewWindow(ViewScope, -0.05, 0.05, 770, time.Second)
	e.LeaseViewWindow(ViewWeb, 0.1, 0.2, 1200, time.Second)
	if a, b, cols, ok := e.viewRequest(ViewScope); !ok || a != -0.05 || b != 0.05 || cols != 770 {
		t.Fatalf("scope request %v %v %v %v", a, b, cols, ok)
	}
	if a, b, cols, ok := e.viewRequest(ViewWeb); !ok || a != 0.1 || b != 0.2 || cols != 1200 {
		t.Fatalf("web request %v %v %v %v", a, b, cols, ok)
	}
	e.LeaseViewWindow(ViewWeb, math.Inf(-1), math.Inf(1), 1200, time.Second)
	if _, _, _, ok := e.viewRequest(ViewWeb); ok {
		t.Fatal("an infinite request kept the web's lease")
	}
	now = now.Add(2 * time.Second)
	if _, _, _, ok := e.viewRequest(ViewScope); ok {
		t.Fatal("the scope's lease outlived its time")
	}
	e.LeaseViewWindow(ViewSurface(7), 0, 1, 770, time.Second) // no such surface: ignored
}
