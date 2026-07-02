package cal

import (
	"encoding/binary"
	"math"
	"testing"
)

// buildBlob creates a valid scrambled blob from a de-scrambled payload.
func buildBlob(t *testing.T, payload []byte) []byte {
	t.Helper()
	if len(payload) != payloadSize {
		t.Fatalf("payload size %d", len(payload))
	}
	scr := make([]byte, payloadSize)
	copy(scr, payload)
	Scramble(scr)
	blob := make([]byte, fileSize)
	binary.LittleEndian.PutUint32(blob[0:4], Checksum(scr))
	copy(blob[4:], scr)
	return blob
}

func TestScrambleRoundTrip(t *testing.T) {
	payload := make([]byte, payloadSize)
	for i := range payload {
		payload[i] = byte(i * 7)
	}
	scr := make([]byte, payloadSize)
	copy(scr, payload)
	Scramble(scr)
	descramble(scr)
	for i := range payload {
		if scr[i] != payload[i] {
			t.Fatalf("round trip differs at %d", i)
		}
	}
}

func TestTriangularIndices(t *testing.T) {
	buf := make([]byte, 32)
	notTriangular(buf)
	// pos = 1, 3, 6, 10, 15, 21, 28 (start 1, step 2,3,4,...).
	want := map[int]bool{1: true, 3: true, 6: true, 10: true, 15: true, 21: true, 28: true}
	for i, v := range buf {
		flipped := v == 0xff
		if flipped != want[i] {
			t.Fatalf("index %d flipped=%v want %v", i, flipped, want[i])
		}
	}
}

func TestParseWorkedOffsets(t *testing.T) {
	// Records at the spec's worked offsets: CH0 vd0 → 0, CH0 vd11 → 0x58,
	// CH1 vd0 → 0x60, CH1 vd11 → 0xb8.
	payload := make([]byte, payloadSize)
	put := func(off int, dac, zero int16, gain float32) {
		binary.LittleEndian.PutUint16(payload[off:], uint16(dac))
		binary.LittleEndian.PutUint16(payload[off+2:], uint16(zero))
		binary.LittleEndian.PutUint32(payload[off+4:], math.Float32bits(gain))
	}
	put(0, 57, 10223, 1.719)
	put(0x58, 5, 10300, 0.169)
	put(0x60, 58, 10100, 1.720)
	put(0xb8, 6, 10400, 0.170)

	tab, err := Parse(buildBlob(t, payload))
	if err != nil {
		t.Fatal(err)
	}
	if r := tab.Rec[0][0]; r.GainDAC != 57 || r.Zero != 10223 || r.Gain != 1.719 {
		t.Fatalf("ch0 vd0 = %+v", r)
	}
	if r := tab.Rec[0][11]; r.GainDAC != 5 || r.Zero != 10300 {
		t.Fatalf("ch0 vd11 = %+v", r)
	}
	if r := tab.Rec[1][0]; r.GainDAC != 58 || r.Zero != 10100 {
		t.Fatalf("ch1 vd0 = %+v", r)
	}
	if r := tab.Rec[1][11]; r.GainDAC != 6 || r.Zero != 10400 {
		t.Fatalf("ch1 vd11 = %+v", r)
	}
}

func TestChecksumRejects(t *testing.T) {
	payload := make([]byte, payloadSize)
	blob := buildBlob(t, payload)
	blob[100] ^= 0x01 // corrupt one payload byte
	if _, err := Parse(blob); err == nil {
		t.Fatal("corrupted blob accepted")
	}
	if _, err := Parse(blob[:100]); err == nil {
		t.Fatal("short blob accepted")
	}
}

func TestDefaults(t *testing.T) {
	d := Defaults()
	if d.Source != "defaults" {
		t.Fatal("source")
	}
	if d.Rec[0][8].Zero != 0x27ef || d.Rec[1][8].Zero != 0x27ef {
		t.Fatalf("default zero = %d, want 10223", d.Rec[0][8].Zero)
	}
	// The per-range break at index 4→5 must be present, not interpolated.
	if !(d.Rec[0][4].Gain < 1 && d.Rec[0][5].Gain > 16) {
		t.Fatalf("gain break missing: %v %v", d.Rec[0][4].Gain, d.Rec[0][5].Gain)
	}
}

func TestDCVolts(t *testing.T) {
	d := Defaults()
	// 1 V/div (vd 8, gain 1.719): mean 238 → (110)·1.719/110 = 1.719 V.
	got := d.DCVolts(0, 8, 238)
	if math.Abs(got-1.719) > 1e-6 {
		t.Fatalf("DCVolts = %v", got)
	}
}
