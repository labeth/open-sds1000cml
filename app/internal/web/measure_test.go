package web

import (
	"math"
	"testing"
)

func TestMeasureSquare(t *testing.T) {
	// 1 kHz square, 800 ns/sample: period 1250 samples, 40↔210 codes.
	sig := make([]uint8, 5000)
	for i := range sig {
		if (i/625)%2 == 0 {
			sig[i] = 40
		} else {
			sig[i] = 210
		}
	}
	// 1 V/div → 1/32 V per code, no offset.
	m := measure(sig, 1.0/32, 0, 800e-9)
	if m == nil {
		t.Fatal("no measurement")
	}
	// Vpp = 170 codes × 1/32 = 5.3125 V.
	if math.Abs(m.Vpp-170.0/32) > 1e-6 {
		t.Fatalf("Vpp = %v, want %v", m.Vpp, 170.0/32)
	}
	// Vmax = (210-128)/32, Vmin = (40-128)/32.
	if math.Abs(m.Vmax-82.0/32) > 1e-6 || math.Abs(m.Vmin-(-88.0/32)) > 1e-6 {
		t.Fatalf("Vmax/Vmin = %v/%v", m.Vmax, m.Vmin)
	}
	// Frequency ≈ 1 kHz.
	if m.Freq < 950 || m.Freq > 1050 {
		t.Fatalf("Freq = %v, want ≈1000", m.Freq)
	}
	// Duty ≈ 50 %.
	if m.Duty < 45 || m.Duty > 55 {
		t.Fatalf("Duty = %v", m.Duty)
	}
}

func TestMeasureFlat(t *testing.T) {
	sig := make([]uint8, 500)
	for i := range sig {
		sig[i] = 128
	}
	m := measure(sig, 1.0/32, 0, 800e-9)
	if m.Vpp != 0 || m.Freq != 0 {
		t.Fatalf("flat: vpp=%v freq=%v", m.Vpp, m.Freq)
	}
	if m := measure(nil, 1, 0, 1); m != nil {
		t.Fatal("empty record produced a measurement")
	}
}

func TestMeasureOffsetReferred(t *testing.T) {
	// A DC level at code 160 with a +1 V applied offset: input = displayed − offset.
	sig := make([]uint8, 100)
	for i := range sig {
		sig[i] = 160
	}
	m := measure(sig, 1.0/32, 1.0, 800e-9)
	// (160−128)/32 − 1 = 1.0 − 1.0 = 0.
	if math.Abs(m.Vmean) > 1e-9 {
		t.Fatalf("offset-referred Vmean = %v, want 0", m.Vmean)
	}
}

func TestResampleEnv(t *testing.T) {
	v := make([]uint8, 800)
	for i := range v {
		v[i] = uint8(i % 256)
	}
	out := resampleEnv(v, 800, 400)
	if len(out) != 400 || out[0] != 0 || out[399] != int16(v[399*800/400]) {
		t.Fatalf("resample: len=%d out[399]=%d", len(out), out[399])
	}
}
