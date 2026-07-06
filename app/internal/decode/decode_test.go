package decode

import (
	"fmt"
	"testing"
)

// uartWave synthesizes an 8N1 LSB-first UART waveform: idle-high, one start bit,
// 8 data bits, one stop bit, at spb samples/bit, with lead/trail idle.
func uartWave(bytes []int, spb int) []uint8 {
	lo, hi := uint8(40), uint8(210)
	var w []uint8
	push := func(bit int, k int) {
		v := lo
		if bit == 1 {
			v = hi
		}
		for j := 0; j < k; j++ {
			w = append(w, v)
		}
	}
	push(1, spb*8) // lead idle
	for _, b := range bytes {
		push(0, spb) // start
		for c := 0; c < 8; c++ {
			push((b>>c)&1, spb) // LSB first
		}
		push(1, spb) // stop
		push(1, spb) // inter-byte idle
	}
	push(1, spb*8) // trail idle
	return w
}

func TestDecodeUARTRoundTrip(t *testing.T) {
	want := []int{0x48, 0x69, 0x20, 0x55, 0xAA, 0x0F, 0xF0, 0x0A} // "Hi " + patterns
	spb := 40
	w := uartWave(want, spb)
	colTimeS := 1e-6 // arbitrary; baud is derived: 1/(spb*colTimeS)
	baud := int(1.0 / (float64(spb) * colTimeS))

	// explicit baud
	r := DecodeUART(w, colTimeS, UARTCfg{Baud: baud})
	if !r.OK {
		t.Fatalf("explicit-baud decode failed: %s", r.Error)
	}
	if got := fmt.Sprintf("%v", r.Bytes); got != fmt.Sprintf("%v", want) {
		t.Errorf("explicit baud: got %v want %v", r.Bytes, want)
	}

	// auto baud
	ra := DecodeUART(w, colTimeS, UARTCfg{})
	if !ra.OK {
		t.Fatalf("auto-baud decode failed: %s", ra.Error)
	}
	if fmt.Sprintf("%v", ra.Bytes) != fmt.Sprintf("%v", want) {
		t.Errorf("auto baud: got %v want %v", ra.Bytes, want)
	}
	if ra.SPB < 30 || ra.SPB > 50 {
		t.Errorf("auto SPB %.1f not near %d", ra.SPB, spb)
	}
}

func TestDecodeUARTFrameError(t *testing.T) {
	// A byte with the stop bit corrupted -> frame-error span with "!" prefix.
	spb := 30
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
	push(1, spb*8)
	push(0, spb)             // start
	for c := 0; c < 8; c++ { // data 0x55
		push((0x55>>c)&1, spb)
	}
	push(0, spb) // BAD stop (should be high)
	push(1, spb*8)
	r := DecodeUART(w, 1e-6, UARTCfg{Baud: int(1.0 / (float64(spb) * 1e-6))})
	if !r.OK || len(r.Spans) == 0 {
		t.Fatalf("decode failed: %s", r.Error)
	}
	if r.Spans[0].Kind != "frame-error" {
		t.Errorf("expected frame-error, got kind=%s text=%q", r.Spans[0].Kind, r.Spans[0].Text)
	}
}
