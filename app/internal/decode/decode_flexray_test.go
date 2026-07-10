package decode

import (
	"fmt"
	"math/rand"
	"testing"
)

// flexrayWave synthesizes a single-ended FlexRay line at spb samples/bit. Idle
// sits HIGH. Each frame is: TSS (tssBits of LOW) -> FSS (1 HIGH bit) -> for every
// byte a BSS (1 HIGH bit, 1 LOW bit) then 8 data bits MSB-first -> FES (1 LOW,
// 1 HIGH bit). Frames are separated by idle HIGH. Mirrors the on-wire signal a
// FlexRay node would drive.
func flexrayWave(frames [][]int, spb, tssBits int) []uint8 {
	lo, hi := uint8(40), uint8(210)
	var w []uint8
	push := func(bit, k int) {
		v := lo
		if bit == 1 {
			v = hi
		}
		for j := 0; j < k; j++ {
			w = append(w, v)
		}
	}
	push(1, spb*8) // lead idle (HIGH)
	for _, bytes := range frames {
		push(0, spb*tssBits) // TSS (long LOW run)
		push(1, spb)         // FSS (1 HIGH bit)
		for _, b := range bytes {
			push(1, spb) // BSS bit0 (HIGH)
			push(0, spb) // BSS bit1 (LOW)
			for d := 7; d >= 0; d-- {
				push((b>>d)&1, spb) // data, MSB-first
			}
		}
		push(0, spb)   // FES low
		push(1, spb)   // FES high
		push(1, spb*8) // inter-frame / trail idle (HIGH)
	}
	return w
}

// headerNote reproduces the decoder's 5-byte-header split so the test can assert
// the emitted note byte-for-byte (passing-by-construction).
func headerNote(b []int) string {
	var hdr uint64
	for i := 0; i < 5; i++ {
		hdr = (hdr << 8) | uint64(b[i]&0xff)
	}
	frameID := int((hdr >> 24) & 0x7FF)
	payloadLen := int((hdr >> 17) & 0x7F)
	cycle := int(hdr & 0x3F)
	note := fmt.Sprintf("ID=%d LEN=%d CYC=%d", frameID, payloadLen, cycle)
	if (hdr>>36)&1 == 1 {
		note += " SYNC"
	}
	if (hdr>>35)&1 == 1 {
		note += " STARTUP"
	}
	return note
}

func TestDecodeFlexRayRoundTrip(t *testing.T) {
	want := []int{0x12, 0x34, 0x56, 0x78, 0x9A, 0xBC, 0xDE, 0xF0} // 5 header + 3 payload
	spb := 20
	colTimeS := 5e-9 // 200 MSa/s
	bitrate := int(1.0 / (float64(spb) * colTimeS))
	w := flexrayWave([][]int{want}, spb, 8)

	// explicit bitrate
	r := DecodeFlexRay(w, colTimeS, FlexRayCfg{Bitrate: bitrate})
	if !r.OK {
		t.Fatalf("explicit-bitrate decode failed: %s", r.Error)
	}
	if got := fmt.Sprintf("%v", r.Bytes); got != fmt.Sprintf("%v", want) {
		t.Errorf("explicit bitrate: got %v want %v", r.Bytes, want)
	}
	if r.Proto != "flexray" {
		t.Errorf("proto = %q, want flexray", r.Proto)
	}

	// auto bitrate (infer T from the edge statistics)
	ra := DecodeFlexRay(w, colTimeS, FlexRayCfg{})
	if !ra.OK {
		t.Fatalf("auto-bitrate decode failed: %s", ra.Error)
	}
	if got := fmt.Sprintf("%v", ra.Bytes); got != fmt.Sprintf("%v", want) {
		t.Errorf("auto bitrate: got %v want %v", ra.Bytes, want)
	}
	if ra.SPB < float64(spb)-4 || ra.SPB > float64(spb)+4 {
		t.Errorf("auto SPB %.1f not near %d", ra.SPB, spb)
	}

	// span checks: a TSS start span, data spans, and the 5-byte header note.
	var kinds string
	sawTSS, sawHdr := false, false
	wantNote := headerNote(want)
	for _, s := range r.Spans {
		kinds += s.Kind + " "
		if s.Kind == "start" && s.Text == "TSS" {
			sawTSS = true
		}
		if s.Kind == "addr" && s.Text == wantNote {
			sawHdr = true
		}
	}
	if !sawTSS {
		t.Errorf("expected a TSS/start span; got kinds: %s", kinds)
	}
	if !containsWord(kinds, "data") {
		t.Errorf("expected data spans; got kinds: %s", kinds)
	}
	if !sawHdr {
		t.Errorf("expected header note span %q; spans: %+v", wantNote, r.Spans)
	}
}

func TestDecodeFlexRayMultiFrame(t *testing.T) {
	// Two frames in one record, separated by idle — segmentation must recover
	// both, in order, with a gap between them. Second frame's header is all-zero
	// data to exercise the long in-frame LOW run NOT being read as a new TSS.
	f1 := []int{0x81, 0x02, 0x03, 0x04, 0x05, 0xAA, 0x55}
	f2 := []int{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x0F}
	spb := 16
	colTimeS := 1e-8
	w := flexrayWave([][]int{f1, f2}, spb, 6)

	r := DecodeFlexRay(w, colTimeS, FlexRayCfg{Bitrate: int(1.0 / (float64(spb) * colTimeS))})
	if !r.OK {
		t.Fatalf("multi-frame decode failed: %s", r.Error)
	}
	all := append(append([]int{}, f1...), f2...)
	if got := fmt.Sprintf("%v", r.Bytes); got != fmt.Sprintf("%v", all) {
		t.Errorf("multi-frame bytes: got %v want %v", r.Bytes, all)
	}
	starts, gaps := 0, 0
	for _, s := range r.Spans {
		if s.Kind == "start" {
			starts++
		}
		if s.Kind == "gap" {
			gaps++
		}
	}
	if starts != 2 {
		t.Errorf("expected 2 TSS/start spans, got %d", starts)
	}
	if gaps != 1 {
		t.Errorf("expected 1 inter-frame gap span, got %d", gaps)
	}
}

func TestDecodeFlexRayPartialAtStart(t *testing.T) {
	// A record that begins in the middle of the TSS LOW (no captured idle->TSS
	// falling edge) is a frame truncated at the record start: it must be dropped,
	// and a following whole frame still recovered.
	good := []int{0x11, 0x22, 0x33, 0x44, 0x55, 0x66}
	spb := 16
	colTimeS := 1e-8
	full := flexrayWave([][]int{{0x99, 0x88, 0x77, 0x66, 0x55}, good}, spb, 8)
	// chop off the lead idle + part of the first TSS so the capture opens LOW
	chop := spb*8 + spb*3
	trunc := full[chop:]

	r := DecodeFlexRay(trunc, colTimeS, FlexRayCfg{Bitrate: int(1.0 / (float64(spb) * colTimeS))})
	if !r.OK {
		t.Fatalf("decode failed: %s", r.Error)
	}
	if got := fmt.Sprintf("%v", r.Bytes); got != fmt.Sprintf("%v", good) {
		t.Errorf("partial-at-start: got %v want only the whole frame %v", r.Bytes, good)
	}
}

// TestDecodeFlexRayNoPanic feeds degenerate/hostile inputs — a decoder must
// return an error, never panic or hang.
func TestDecodeFlexRayNoPanic(t *testing.T) {
	rng := rand.New(rand.NewSource(0xF1E))
	mk := func(n, kind int) []uint8 {
		s := make([]uint8, n)
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
		case 3: // long low run then noise (TSS-like bait)
			for i := range s {
				if i < n/2 {
					s[i] = 20
				} else {
					s[i] = uint8(rng.Intn(256))
				}
			}
		}
		return s
	}
	inputs := [][]uint8{nil, {}, {200}, mk(5, 2)}
	for n := 0; n < 60; n++ {
		inputs = append(inputs, mk(rng.Intn(4000), rng.Intn(4)))
	}
	colTimes := []float64{0, 1e-12, 1e-8, 1}
	bitrates := []int{0, -100, 10000000, 1 << 30}
	for _, in := range inputs {
		for _, ct := range colTimes {
			cfg := FlexRayCfg{
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
				r := DecodeFlexRay(in, ct, cfg)
				if !r.OK && r.Error == "" {
					t.Fatalf("degenerate input returned neither OK nor Error")
				}
			}()
		}
	}
}
