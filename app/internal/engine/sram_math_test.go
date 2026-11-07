// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package engine

import (
	"fmt"
	"math"
	"testing"

	"github.com/labeth/open-dso-core/sample"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// TRLC-LINKS: REQ-SDS-012
func TestERESQ8(t *testing.T) {
	for _, n := range []int{1, 2, 9, 100} {
		src := make([]uint16, n)
		want := make([]uint16, n)
		for i := range src {
			src[i] = uint16((i*173 + 257) % 65536)
		}
		for _, l := range []int{3, 15, 63} {
			in := append([]uint16(nil), src...)
			for i := range src {
				sum, count := uint32(0), uint32(0)
				for j := i - l/2; j <= i+l/2; j++ {
					if j >= 0 && j < n {
						sum += uint32(src[j])
						count++
					}
				}
				want[i] = uint16((sum + count/2) / count)
			}
			eresQ8(in, make([]uint16, n), l)
			for i := range in {
				if in[i] != want[i] {
					t.Fatalf("n=%d l=%d i=%d got=%d want=%d", n, l, i, in[i], want[i])
				}
			}
		}
	}
}

// The average is the mean of the records until target are in, then a running
// average in which each record weighs 1/target: a fourth record moves a full
// average of three by a third of its difference, not a restart that shows it
// alone (every target-th frame was one raw capture).
// TRLC-LINKS: REQ-SDS-012
func TestSRAMAverageFractionsRunningAndReset(t *testing.T) {
	var a sramAverage
	for i, want := range []uint16{25600, 25728, 25856, 26027} {
		q := uint16(25600 + i*256)
		f := sample.Frame{Traces: []sample.Trace{{Valid: 1, Codes8: make([]byte, 1), Precision: []uint16{q}}, {Ch: 1, Valid: 1, Codes8: make([]byte, 1), Precision: []uint16{65535}}}}
		if got := a.push(&f, 3); got != min(i+1, 3) || f.Precision(0)[0] != want || f.Precision(1)[0] != 65535 {
			t.Fatalf("step %d: holds %d, %v %v", i, got, f.Precision(0), f.Precision(1))
		}
	}
	a.reset()
	f := sample.Frame{Traces: []sample.Trace{{Valid: 1, Codes8: make([]byte, 1), Precision: []uint16{1}}, {Ch: 1, Valid: 1, Codes8: make([]byte, 1), Precision: []uint16{2}}}}
	if a.push(&f, 256) != 1 || f.Precision(0)[0] != 1 {
		t.Fatal("reset retained prior capture")
	}
	for i := 1; i < 256; i++ {
		f.Precision(0)[0] = 65535
		f.Precision(1)[0] = 65535
		a.push(&f, 256)
	}
	if f.Precision(0)[0] != 65279 {
		t.Fatalf("256-frame sum: %d", f.Precision(0)[0])
	}
}

// TRLC-LINKS: REQ-SDS-012
func TestSRAMAverageAlignsShiftedRecordWithoutWrapping(t *testing.T) {
	var a sramAverage
	for k, shift := range []int{0, 3, -2} {
		f := sample.Frame{TriggerAt: float64(50 + shift), Traces: []sample.Trace{{Valid: 100, Codes8: make([]byte, 100), Precision: make([]uint16, 100)}, {Ch: 1, Valid: 100, Codes8: make([]byte, 100), Precision: make([]uint16, 100)}}}
		for i := range f.Precision(0) {
			f.Precision(0)[i] = uint16(10000 + (i-shift)*128)
			f.Precision(1)[i] = uint16(20000 - (i-shift)*64)
		}
		a.push(&f, 3)
		for i := range f.Len() {
			if f.Precision(0)[i] != uint16(10000+(i+a.lo)*128) || f.Precision(1)[i] != uint16(20000-(i+a.lo)*64) {
				t.Fatalf("frame %d sample %d alignment failed", k, i)
			}
		}
		if k == 2 && (f.Len() != 95 || f.TriggerAt != 48) {
			t.Fatalf("invalid common region: %d %g", f.Len(), f.TriggerAt)
		}
	}
}

// TRLC-LINKS: REQ-SDS-012
func TestAverageAlignsToFractionOfSample(t *testing.T) {
	ramp := func(edge float64) sample.Frame {
		n := 64
		f := sample.Frame{TriggerAt: edge, Traces: []sample.Trace{{Valid: n, Codes8: make([]byte, n), Precision: make([]uint16, n)}, {Ch: 1, Valid: n, Codes8: make([]byte, n), Precision: make([]uint16, n)}}}
		for i := range n {
			v := 128 + 4*(float64(i)-edge)
			f.Precision(0)[i] = uint16(math.Round(v * 256))
			f.Precision(1)[i] = f.Precision(0)[i]
			f.Codes8(0)[i], f.Codes8(1)[i] = roundQ8(f.Precision(0)[i]), roundQ8(f.Precision(1)[i])
		}
		return f
	}
	var a sramAverage
	first := ramp(30)
	a.push(&first, 2)
	second := ramp(30.5)
	if a.push(&second, 2) != 2 {
		t.Fatal("second record not accumulated")
	}
	// Both records were the same ramp, so the average is that ramp anchored at
	// the first edge: code 128 exactly at EdgeX.
	i := int(second.TriggerAt)
	if second.TriggerAt != 30 || math.Abs(float64(second.Precision(0)[i])/256-128) > 0.05 {
		t.Fatalf("edge %.2f, value there %.3f, want 30 and 128", second.TriggerAt, float64(second.Precision(0)[i])/256)
	}
}

// A decimated record a sample or two longer or shorter than the block's (its
// length moves with the trigger's phase: 14515 to 14517 samples at 20us/div)
// joins the block over the samples both hold, or the average restarts on each
// change, never reaches 64, and a master never finishes. A record of another
// plan still starts a new block.
// TRLC-LINKS: REQ-SDS-012
func TestSRAMAverageKeepsItsBlockOverALengthWobble(t *testing.T) {
	var a sramAverage
	frame := func(n int) *sample.Frame {
		f := &sample.Frame{TriggerAt: 500, Traces: []sample.Trace{{Valid: n, Codes8: make([]byte, n), Precision: make([]uint16, n)}, {Ch: 1, Valid: n, Codes8: make([]byte, n), Precision: make([]uint16, n)}}}
		for i := range f.Precision(0) {
			f.Precision(0)[i], f.Precision(1)[i] = uint16(i%200)<<8, 128<<8
		}
		return f
	}
	var count int
	for k, n := range []int{14517, 14516, 14515, 14517, 14516, 14517, 14515, 14516} {
		f := frame(n)
		count = a.push(f, 64)
		if count != k+1 {
			t.Fatalf("capture %d (%d samples): the block counts %d", k, n, count)
		}
		if f.Len() > n || f.Len() < 14515 || f.Precision(0)[300] != 100<<8 {
			t.Fatalf("capture %d: %d samples, Q1[300] %d", k, f.Len(), f.Precision(0)[300])
		}
	}
	if count = a.push(frame(7258), 64); count != 1 {
		t.Fatalf("another plan's record joined the block: %d", count)
	}
}

// An average's block keeps to the same settings' captures, not to the record's
// split around the trigger, which a serial trigger moves by each match's
// measured shift (keyed on the split, the block restarts on almost every
// capture).
// TRLC-LINKS: REQ-SDS-012
func TestAverageBlockIgnoresTheTriggerSplit(t *testing.T) {
	c := sramcapture.Config{DecimationLog2: 4, PreWords: 7259, PostWords: 7258, TriggerLevel: 80}
	moved := c
	moved.PreWords, moved.PostWords = 7260, 7257
	if fmt.Sprint(averageCfg(c)) != fmt.Sprint(averageCfg(moved)) {
		t.Fatal("a moved trigger split started a new block")
	}
	level := c
	level.TriggerLevel = 90
	if fmt.Sprint(averageCfg(c)) == fmt.Sprint(averageCfg(level)) {
		t.Fatal("another trigger level kept the block")
	}
}
