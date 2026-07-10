package decode

import (
	"fmt"
	"math/rand"
	"testing"
)

// arincMakeWord builds the 32 transmission-order bits of an ARINC 429 word from
// its fields and appends a correct ODD parity bit (bit 32). bits[0] is the first
// bit on the wire (ARINC #1). label8 is packed LSB-first into bits[0..7] (so the
// decoder's bit-reversal reproduces the 3-digit octal label).
func arincMakeWord(label8, sdi2, data19, ssm2 int) []int {
	bits := make([]int, 32)
	for i := 0; i < 8; i++ {
		bits[i] = (label8 >> i) & 1
	}
	bits[8] = sdi2 & 1
	bits[9] = (sdi2 >> 1) & 1
	for i := 0; i < 19; i++ {
		bits[10+i] = (data19 >> i) & 1
	}
	bits[29] = ssm2 & 1
	bits[30] = (ssm2 >> 1) & 1
	ones := 0
	for i := 0; i < 31; i++ {
		ones += bits[i]
	}
	if ones%2 == 0 { // make the total (incl. parity) odd
		bits[31] = 1
	}
	return bits
}

// arincExpect mirrors the decoder's field extraction so the test asserts against
// values derived from the SAME bit stream (round-trip by construction).
func arincExpect(bits []int) (labelOct, dataHex, ssmTxt string, bytesLE []int) {
	labelRev := 0
	for i := 0; i < 8; i++ {
		labelRev = (labelRev << 1) | bits[i]
	}
	dataVal := 0
	for i := 0; i < 19; i++ {
		dataVal |= bits[10+i] << i
	}
	ssm := bits[29] | bits[30]<<1
	word32 := 0
	for i := 0; i < 32; i++ {
		word32 |= bits[i] << i
	}
	labelOct = fmt.Sprintf("%03o", labelRev&0xff)
	dataHex = fmt.Sprintf("%05X", dataVal&0x7ffff)
	ssmTxt = fmt.Sprintf("SSM%d", ssm)
	bytesLE = []int{word32 & 0xff, (word32 >> 8) & 0xff, (word32 >> 16) & 0xff, (word32 >> 24) & 0xff}
	return
}

// arincAppendWord renders 32 (or fewer, for a partial) bits as a bipolar RZ pulse
// train: each bit is a HI(1)/LO(0) pulse for the first half of the cell then a
// return to NULL. NULL=128, HI=210, LO=40.
func arincAppendWord(w *[]uint8, bits []int, spb int) {
	const null_, hi, lo = uint8(128), uint8(210), uint8(40)
	half := spb / 2
	for _, b := range bits {
		pv := lo
		if b == 1 {
			pv = hi
		}
		for j := 0; j < half; j++ {
			*w = append(*w, pv)
		}
		for j := 0; j < spb-half; j++ {
			*w = append(*w, null_)
		}
	}
}

func arincIdle(w *[]uint8, k int) {
	for j := 0; j < k; j++ {
		*w = append(*w, 128)
	}
}

func TestDecodeARINC429RoundTrip(t *testing.T) {
	spb := 40
	colTimeS := 2.5e-7                              // -> 100 kbit/s at spb=40
	bitrate := int(1.0 / (float64(spb) * colTimeS)) // 100000

	w1 := arincMakeWord(0312, 1 /*SDI*/, 0x2ABCD /*data19*/, 3 /*SSM*/)
	w2 := arincMakeWord(0107, 2, 0x15A3F, 1)
	lead := []int{1, 0, 1, 1, 0, 0, 1, 0, 1, 0} // a partial fragment (10 bits) at the record start
	trail := []int{0, 1, 1, 0, 0, 1, 1, 0}      // a partial fragment (8 bits) at the record end

	var w []uint8
	arincIdle(&w, spb*6)
	arincAppendWord(&w, lead, spb) // leading partial — must be dropped
	arincIdle(&w, spb*6)
	arincAppendWord(&w, w1, spb)
	arincIdle(&w, spb*6)
	arincAppendWord(&w, w2, spb)
	arincIdle(&w, spb*6)
	arincAppendWord(&w, trail, spb) // trailing partial — must be dropped
	arincIdle(&w, spb*6)

	// explicit bitrate
	r := DecodeARINC429(w, colTimeS, ARINC429Cfg{Bitrate: bitrate})
	if !r.OK {
		t.Fatalf("explicit-bitrate decode failed: %s", r.Error)
	}
	if r.Proto != "arinc429" {
		t.Errorf("proto = %q, want arinc429", r.Proto)
	}
	// Exactly the two complete words survive (partials dropped) => 8 raw bytes.
	l1, d1, s1, b1 := arincExpect(w1)
	l2, d2, s2, b2 := arincExpect(w2)
	wantBytes := append(append([]int{}, b1...), b2...)
	if got := fmt.Sprintf("%v", r.Bytes); got != fmt.Sprintf("%v", wantBytes) {
		t.Errorf("bytes: got %v want %v", r.Bytes, wantBytes)
	}

	// Field spans: two labels (octal), two data (hex), two SSM, no parity error.
	var labels, datas, ssms []string
	parityErrs := 0
	for _, sp := range r.Spans {
		switch sp.Kind {
		case "addr":
			labels = append(labels, sp.Text)
		case "data":
			datas = append(datas, sp.Text)
		case "rw":
			ssms = append(ssms, sp.Text)
		case "frame-error":
			parityErrs++
		}
	}
	if fmt.Sprintf("%v", labels) != fmt.Sprintf("%v", []string{l1, l2}) {
		t.Errorf("labels: got %v want %v", labels, []string{l1, l2})
	}
	if fmt.Sprintf("%v", datas) != fmt.Sprintf("%v", []string{d1, d2}) {
		t.Errorf("data: got %v want %v", datas, []string{d1, d2})
	}
	if fmt.Sprintf("%v", ssms) != fmt.Sprintf("%v", []string{s1, s2}) {
		t.Errorf("ssm: got %v want %v", ssms, []string{s1, s2})
	}
	if parityErrs != 0 {
		t.Errorf("unexpected %d parity/frame errors on good words", parityErrs)
	}

	// auto bitrate: infer T from the pulse spacing.
	ra := DecodeARINC429(w, colTimeS, ARINC429Cfg{})
	if !ra.OK {
		t.Fatalf("auto-bitrate decode failed: %s", ra.Error)
	}
	if fmt.Sprintf("%v", ra.Bytes) != fmt.Sprintf("%v", wantBytes) {
		t.Errorf("auto bitrate: got %v want %v", ra.Bytes, wantBytes)
	}
	if ra.SPB < float64(spb)-4 || ra.SPB > float64(spb)+4 {
		t.Errorf("auto SPB %.1f not near %d", ra.SPB, spb)
	}
	if ra.Baud < bitrate*9/10 || ra.Baud > bitrate*11/10 {
		t.Errorf("auto baud %d not near %d", ra.Baud, bitrate)
	}
}

func TestDecodeARINC429ParityError(t *testing.T) {
	spb := 40
	colTimeS := 2.5e-7
	bitrate := int(1.0 / (float64(spb) * colTimeS))
	bits := arincMakeWord(0250, 0, 0x3C0F0, 2)
	bits[31] ^= 1 // corrupt the parity bit -> odd parity now violated

	var w []uint8
	arincIdle(&w, spb*6)
	arincAppendWord(&w, bits, spb)
	arincIdle(&w, spb*6)

	r := DecodeARINC429(w, colTimeS, ARINC429Cfg{Bitrate: bitrate})
	if !r.OK {
		t.Fatalf("decode failed: %s", r.Error)
	}
	sawErr := false
	for _, sp := range r.Spans {
		if sp.Kind == "frame-error" {
			sawErr = true
		}
	}
	if !sawErr {
		t.Errorf("expected a frame-error span for the bad parity bit; spans=%v", r.Spans)
	}
}

// TestDecodeARINC429NoPanic feeds degenerate/hostile inputs — the decoder must
// return an error, never panic or hang.
func TestDecodeARINC429NoPanic(t *testing.T) {
	rng := rand.New(rand.NewSource(429))
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
		case 2: // flat NULL
			for i := range s {
				s[i] = 128
			}
		case 3: // slow tri-level churn
			for i := range s {
				s[i] = []uint8{40, 128, 210, 128}[(i/7)%4]
			}
		}
		return s
	}
	inputs := [][]uint8{nil, {}, {200}, mk(3, 2)}
	for k := 0; k < 60; k++ {
		inputs = append(inputs, mk(rng.Intn(3000), rng.Intn(4)))
	}
	colTimes := []float64{0, 1e-12, 2.5e-7, 1}
	bitrates := []int{0, -100, 100000, 1 << 30}
	for _, in := range inputs {
		for _, ct := range colTimes {
			cfg := ARINC429Cfg{
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
				r := DecodeARINC429(in, ct, cfg)
				if !r.OK && r.Error == "" {
					t.Fatalf("degenerate input returned neither OK nor Error")
				}
			}()
		}
	}
}
