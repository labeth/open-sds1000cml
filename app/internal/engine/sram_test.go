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
