// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"testing"

	"open-sds/app/internal/sramcapture"
)

// TRLC-LINKS: REQ-SDS-010
func TestEnvelopeWindow(t *testing.T) {
	// A small window is recalled as is.
	if b, s, n := envelopeWindow(100, 2047, 2048); b != 0 || s != 100 || n != 2047 {
		t.Fatalf("small window: %d %d %d", b, s, n)
	}
	// A whole raw record, short of the warm-up words: 254 words per bucket, centred.
	if b, s, n := envelopeWindow(0, sramcapture.Words, 2048); b != 254 || n != 254*2048 || s != 8+(sramcapture.Words-16-n)/2 {
		t.Fatalf("full record: %d %d %d", b, s, n)
	}
	// An uneven window keeps whole buckets, centred on the original.
	if b, s, n := envelopeWindow(1000, 250511, 2048); b != 122 || n != 122*2048 || s != 1000+(250511-n)/2 {
		t.Fatalf("uneven window: %d %d %d", b, s, n)
	}
	if b, s, n := envelopeWindow(0, 3*1024+7, 2048); b != 0 || n != 3*1024+7 || s != 0 {
		t.Fatalf("short window: %d %d %d", b, s, n)
	}
	if b, s, n := envelopeWindow(0, 9*2048+7, 2048); b != 8 || n != 8*2048 || s != (9*2048+7-n)/2 {
		t.Fatalf("uneven window: %d %d %d", b, s, n)
	}
}

// TRLC-LINKS: REQ-SDS-010
func TestEnvelopeAllowed(t *testing.T) {
	e := &Engine{hardwareEnvelope: true}
	edge := trigParams{typ: TrigEdge}
	if !e.envelopeAllowed(sramcapture.Config{}, edge) {
		t.Fatal("plain edge capture refused")
	}
	e.SetBodeMode(true, 0, 1)
	if e.envelopeAllowed(sramcapture.Config{}, edge) {
		t.Fatal("Bode needs time-ordered samples, not extrema")
	}
	e.SetBodeMode(false, 0, 1)
	// Masks, decode and software qualifiers run on the time-ordered envelope.
	e.SetMaskMode(MaskTest)
	if !e.envelopeAllowed(sramcapture.Config{}, edge) {
		t.Fatal("mask testing refused the live envelope")
	}
	e.SetMaskMode(MaskOff)
	e.SetDecodeView(true)
	if !e.envelopeAllowed(sramcapture.Config{}, trigParams{typ: TrigPulse}) {
		t.Fatal("decode / pulse qualification refused the live envelope")
	}
	e.SetDecodeView(false)
	// The noise-reducing modes need true samples: decimated live captures.
	for _, mode := range []int32{AcqAverage, AcqPrecision, AcqEres} {
		e.acqMode.Store(mode)
		if why := e.envelopeBlock(sramcapture.Config{}, edge); why == "" || !liveDecimates(why) {
			t.Fatalf("mode %d: block %q", mode, why)
		}
	}
	e.acqMode.Store(AcqPeak)
	if !e.envelopeAllowed(sramcapture.Config{}, edge) {
		t.Fatal("peak mode refused")
	}
	e.hardwareEnvelope = false
	if e.envelopeAllowed(sramcapture.Config{}, edge) {
		t.Fatal("an image without the envelope recall")
	}
}

// An edge inside a bucket reads as one transition after ordering; a spike
// narrower than a bucket stays a one-sample peak.
// TRLC-LINKS: REQ-SDS-010
func TestOrderEnvelope(t *testing.T) {
	// falling edge in pair 1, rising in pair 3, a spike in pair 5 (min,max order)
	c := []uint8{200, 200, 10, 200, 10, 10, 10, 200, 200, 200, 10, 250, 10, 10}
	q := make([]uint16, len(c))
	for i, v := range c {
		q[i] = uint16(v) << 8
	}
	orderEnvelope(c, q)
	want := []uint8{200, 200, 200, 10, 10, 10, 10, 200, 200, 200, 250, 10, 10, 10}
	for i := range want {
		if c[i] != want[i] || q[i] != uint16(want[i])<<8 {
			t.Fatalf("ordered %v, want %v", c, want)
		}
	}
}
