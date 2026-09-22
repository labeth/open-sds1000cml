package engine

import (
	"encoding/binary"
	"math"
	"open-sds/app/internal/sramcapture"
	"testing"
)

func TestSRAMPlannerCoversEveryTimebase(t *testing.T) {
	for _, td := range SupportedTdivs() {
		p := PlanSRAM(td)
		if float64(p.Samples)*p.SampleS+1e-12 < 10*td {
			t.Fatalf("%g does not fit: %+v", td, p)
		}
		if p.Screen < 2 || p.Screen > p.Samples {
			t.Fatal(p)
		}
		if p.Log == 0 && p.Samples != 1048576 {
			t.Fatal(p)
		}
		if p.Log != 0 && (p.Log < 4 || p.Samples != 524288) {
			t.Fatal(p)
		}
	}
}
func TestSRAMFrameWriterPreservesChannelsAndFractions(t *testing.T) {
	f := &Frame{Valid: 2, C1: make([]uint8, 2), C2: make([]uint8, 2), Q1: make([]uint16, 2), Q2: make([]uint16, 2)}
	w := sramFrameWriter{f: f, m: sramcapture.Metadata{SamplesPerWord: 1, FractionBits: 8}}
	b := make([]byte, 8)
	for i, v := range []uint16{25601, 32768, 65535, 1} {
		binary.LittleEndian.PutUint16(b[2*i:], v)
	}
	if _, err := w.Write(b); err != nil {
		t.Fatal(err)
	}
	if f.Q1[0] != 25601 || f.Q2[0] != 32768 || f.Q1[1] != 65535 || f.Q2[1] != 1 {
		t.Fatal(f)
	}
	if f.C1[1] != 255 || f.C2[1] != 0 {
		t.Fatal("rounded preview overflow")
	}
	if _, err := w.Write(b); err == nil {
		t.Fatal("accepted overrun")
	}
}
func TestSRAMFrameWriterRawOrder(t *testing.T) {
	f := &Frame{Valid: 2, C1: make([]uint8, 2), C2: make([]uint8, 2)}
	w := sramFrameWriter{f: f, m: sramcapture.Metadata{SamplesPerWord: 2}}
	if _, err := w.Write([]byte{1, 2, 3, 4}); err != nil {
		t.Fatal(err)
	}
	if f.C1[0] != 1 || f.C1[1] != 3 || f.C2[0] != 2 || f.C2[1] != 4 {
		t.Fatal(f)
	}
}
func TestSRAMPlanInvalidInput(t *testing.T) {
	for _, td := range []float64{0, -1, math.NaN(), math.Inf(1)} {
		p := PlanSRAM(td)
		if !(p.SampleS > 0) || p.Screen < 2 {
			t.Fatal(p)
		}
	}
}

func TestSRAMSelectedDepth(t *testing.T) {
	for _, tc := range []struct {
		depth int
		tdiv  float64
		want  int
	}{
		{20000, 5e-7, 1048576}, {1048576, 5e-7, 1048576}, {2000, 5e-7, 1048576},
	} {
		e := &Engine{band: Band{TdivS: tc.tdiv}}
		e.memDepth.Store(int32(tc.depth))
		e.trigPosFrac.Store(math.Float64bits(.5))
		c, p, _, _, _ := e.sramConfig()
		if p.Samples != tc.want || int(c.PreWords+c.PostWords)*2 != tc.want || c.PostWords == 0 {
			t.Fatalf("depth %d: config %+v plan %+v", tc.depth, c, p)
		}
	}
}

func TestLiveSRAMWindowBoundaries(t *testing.T) {
	for _, per := range []uint8{1, 2} {
		for _, length := range []uint32{100, 524288} {
			for _, anchor := range []uint32{0, length / 2, length - 1} {
				for _, screen := range []int{2500, 20000} {
					m := sramcapture.Metadata{Length: length, TriggerIndex: anchor, SamplesPerWord: per}
					off, n := liveSRAMWindow(m, screen)
					if n == 0 || off+n > length || anchor < off || anchor >= off+n {
						t.Fatalf("bad window %+v %d %d", m, off, n)
					}
					if length == 524288 && screen == 2500 && int(n)*int(per) != 3012 {
						t.Fatalf("preview size %d", n)
					}
				}
			}
		}
	}
}

func TestPrecisionRateIndependentOfView(t *testing.T) {
	for _, log := range []uint8{4, 5, 8, 12, 20} {
		for _, td := range []float64{5e-7, 1e-5, 1e-3, 1} {
			p := PlanPrecisionSRAM(td, log)
			if p.Log != log || p.Samples != 524288 || p.SampleS != float64(uint64(1)<<log)/500e6 {
				t.Fatalf("view %g log %d: %+v", td, log, p)
			}
			if p.Screen < 2 || p.Screen > p.Samples {
				t.Fatal(p)
			}
		}
	}
}
func TestPrecisionRateSelection(t *testing.T) {
	e := &Engine{}
	for _, tc := range []struct{ in, want float64 }{{31250000, 31250000}, {15625000, 15625000}, {1e9, 31250000}, {1, 500e6 / (1 << 20)}} {
		if got := e.SetPrecisionRate(tc.in); got != tc.want {
			t.Fatalf("%g: %g", tc.in, got)
		}
	}
}

func TestAllTimebasesRateContract(t *testing.T) {
	for _, td := range []float64{1e-09, 2e-09, 5e-09, 1e-08, 2.5e-08, 5e-08, 1e-07, 2e-07, 5e-07, 1e-06, 2e-06, 5e-06, 1e-05, 2e-05, 5e-05, 0.0001, 0.0002, 0.0005, 0.001, 0.002, 0.005, 0.01, 0.02, 0.05, 0.1, 0.2, 0.5, 1, 2, 5, 10, 20, 50} {
		p := PlanSRAM(td)
		if p.Log == 0 {
			if p.SampleS != 2e-9 || 10*td > float64(p.Samples)*p.SampleS {
				t.Fatal(td, p)
			}
		} else {
			if p.Log < 4 || p.SampleS != float64(uint64(1)<<p.Log)/500e6 || 10*td > float64(p.Samples)*p.SampleS {
				t.Fatal(td, p)
			}
		}
		for log := uint8(4); log <= 20; log++ {
			q := PlanPrecisionSRAM(td, log)
			if q.Log != log || q.SampleS != float64(uint64(1)<<log)/500e6 {
				t.Fatal(td, q)
			}
		}
	}
}
