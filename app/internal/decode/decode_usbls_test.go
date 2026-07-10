package decode

import (
	"fmt"
	"math/rand"
	"testing"
)

// usbPkt is one synthetic packet: a 4-bit PID value and its payload bytes.
type usbPkt struct {
	pid  int
	data []int
}

// usbWave synthesizes the D+ single-ended line for a list of USB packets, at spb
// samples/bit. Each packet is SYNC (00000001) + PID byte (4-bit PID + its
// complement, LSB-first) + data bytes (LSB-first), the whole run bit-stuffed
// (a 0 inserted after six consecutive 1s) then NRZI-encoded from idle J (a 0 bit
// toggles the level, a 1 holds it). EOP is 2 bit-times of SE0 (D+ low), then the
// line returns to idle. This mirrors the on-wire signal a real host/device pair
// would drive, so the decoder round-trips it by construction.
func usbWave(packets []usbPkt, spb int) []uint8 {
	lo, hi := uint8(40), uint8(210)
	const idle = 1 // J = high
	var w []uint8
	push := func(level, cells int) {
		v := lo
		if level == 1 {
			v = hi
		}
		for j := 0; j < cells*spb; j++ {
			w = append(w, v)
		}
	}
	push(idle, 20) // lead idle
	for _, p := range packets {
		// Logical bit stream: SYNC + PID + data (all LSB-first for the byte fields).
		bits := []int{0, 0, 0, 0, 0, 0, 0, 1} // SYNC
		pidByte := (p.pid & 0xF) | ((^p.pid & 0xF) << 4)
		for i := 0; i < 8; i++ {
			bits = append(bits, (pidByte>>i)&1)
		}
		for _, d := range p.data {
			for i := 0; i < 8; i++ {
				bits = append(bits, (d>>i)&1)
			}
		}
		// Bit-stuff: emit a 0 after six consecutive 1s.
		var stuffed []int
		ones := 0
		for _, b := range bits {
			stuffed = append(stuffed, b)
			if b == 1 {
				if ones++; ones == 6 {
					stuffed = append(stuffed, 0)
					ones = 0
				}
			} else {
				ones = 0
			}
		}
		// NRZI-encode from idle: 0 toggles the level, 1 holds it.
		level := idle
		for _, b := range stuffed {
			if b == 0 {
				level = 1 - level
			}
			push(level, 1)
		}
		push(0, 2)     // EOP: 2 bit-times of SE0 (D+ low)
		push(idle, 40) // inter-packet idle
	}
	return w
}

func TestDecodeUSBLSRoundTrip(t *testing.T) {
	// A token (SETUP), a data packet whose payload forces bit stuffing (0xFF), and
	// a handshake (ACK) — the three shapes a real transaction uses.
	packets := []usbPkt{
		{pid: 0xD /*SETUP*/, data: []int{0x12, 0x00}},
		{pid: 0x3 /*DATA0*/, data: []int{0x48, 0x69, 0xFF, 0x0F, 0x7E}},
		{pid: 0x2 /*ACK*/, data: nil},
	}
	spb := 40
	colTimeS := 1.0 / (float64(spb) * 1.5e6) // LS 1.5 Mbit/s at 40 samples/bit
	bitrate := int(1.0 / (float64(spb) * colTimeS))
	w := usbWave(packets, spb)

	wantBytes := []int{0x12, 0x00, 0x48, 0x69, 0xFF, 0x0F, 0x7E} // payload after each PID

	// explicit bitrate
	r := DecodeUSBLS(w, colTimeS, USBLSCfg{Bitrate: bitrate})
	if !r.OK {
		t.Fatalf("explicit-bitrate decode failed: %s", r.Error)
	}
	if got := fmt.Sprintf("%v", r.Bytes); got != fmt.Sprintf("%v", wantBytes) {
		t.Errorf("explicit bitrate: got %v want %v", r.Bytes, wantBytes)
	}
	if r.Proto != "usbls" {
		t.Errorf("proto = %q, want usbls", r.Proto)
	}

	// auto bitrate (infer T from the edge statistics)
	ra := DecodeUSBLS(w, colTimeS, USBLSCfg{})
	if !ra.OK {
		t.Fatalf("auto-bitrate decode failed: %s", ra.Error)
	}
	if got := fmt.Sprintf("%v", ra.Bytes); got != fmt.Sprintf("%v", wantBytes) {
		t.Errorf("auto bitrate: got %v want %v", ra.Bytes, wantBytes)
	}
	if ra.SPB < float64(spb)-4 || ra.SPB > float64(spb)+4 {
		t.Errorf("auto SPB %.1f not near %d", ra.SPB, spb)
	}

	// The span stream must carry a SYNC/start plus the three PID names, in order.
	var syncs int
	var pidNames []string
	for _, s := range r.Spans {
		if s.Kind == "start" && s.Text == "SYNC" {
			syncs++
		}
		if s.Kind == "addr" {
			pidNames = append(pidNames, s.Text)
		}
	}
	if syncs != 3 {
		t.Errorf("expected 3 SYNC spans, got %d", syncs)
	}
	if got := fmt.Sprintf("%v", pidNames); got != "[SETUP DATA0 ACK]" {
		t.Errorf("PID names: got %v want [SETUP DATA0 ACK]", pidNames)
	}
	if !containsWord(usblsKinds(r.Spans), "data") {
		t.Errorf("expected data spans")
	}
}

// usblsKinds concatenates span kinds for a containsWord check.
func usblsKinds(spans []Span) string {
	s := ""
	for _, sp := range spans {
		s += sp.Kind + " "
	}
	return s
}

// TestDecodeUSBLSNoPanic feeds degenerate/hostile inputs — a decoder must return
// an error, never panic or hang (the package also runs a broader decoder fuzz).
func TestDecodeUSBLSNoPanic(t *testing.T) {
	rng := rand.New(rand.NewSource(0x05B)) // deterministic seed
	mk := func(nn, kind int) []uint8 {
		s := make([]uint8, nn)
		switch kind {
		case 0: // noise
			for i := range s {
				s[i] = uint8(rng.Intn(256))
			}
		case 1: // nyquist toggle
			for i := range s {
				s[i] = uint8(i % 2 * 255)
			}
		case 2: // flat
			for i := range s {
				s[i] = 128
			}
		case 3: // fast alternating (ambiguous phase)
			for i := range s {
				s[i] = uint8((i / 3 % 2) * 255)
			}
		}
		return s
	}
	inputs := [][]uint8{nil, {}, {200}, mk(3, 2)}
	for k := 0; k < 60; k++ {
		inputs = append(inputs, mk(rng.Intn(3000), rng.Intn(4)))
	}
	colTimes := []float64{0, 1e-12, 1e-8, 1}
	bitrates := []int{0, -100, 1500000, 12000000, 1 << 30}
	for _, in := range inputs {
		for _, ct := range colTimes {
			cfg := USBLSCfg{
				Bitrate:   bitrates[rng.Intn(len(bitrates))],
				Threshold: rng.Float64() * 260,
				HaveThr:   rng.Intn(2) == 0,
			}
			func() {
				defer func() {
					if p := recover(); p != nil {
						t.Fatalf("panic on cfg %+v: %v", cfg, p)
					}
				}()
				r := DecodeUSBLS(in, ct, cfg)
				if !r.OK && r.Error == "" {
					t.Fatalf("degenerate input returned neither OK nor Error")
				}
			}()
		}
	}
}
