// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"testing"

	"open-sds/app/internal/sramcapture"
)

// TRLC-LINKS: REQ-SDS-010
func TestEnvelopeWindow(t *testing.T) {
	// A small window is recalled as is.
	if b, s, n := envelopeWindow(100, 2047); b != 0 || s != 100 || n != 2047 {
		t.Fatalf("small window: %d %d %d", b, s, n)
	}
	// A whole raw record, short of the warm-up words: 510 words per bucket, centred.
	if b, s, n := envelopeWindow(0, sramcapture.Words); b != 510 || n != 510*1024 || s != 8+(sramcapture.Words-16-n)/2 {
		t.Fatalf("full record: %d %d %d", b, s, n)
	}
	// An uneven window keeps whole buckets, centred on the original.
	if b, s, n := envelopeWindow(1000, 250511); b != 244 || n != 244*1024 || s != 1000+(250511-n)/2 {
		t.Fatalf("uneven window: %d %d %d", b, s, n)
	}
	if b, s, n := envelopeWindow(0, 5*1024+7); b != 0 || n != 5*1024+7 || s != 0 {
		t.Fatalf("short window: %d %d %d", b, s, n)
	}
	if b, s, n := envelopeWindow(0, 9*1024+7); b != 8 || n != 8*1024 || s != (9*1024+7-n)/2 {
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
	for _, mode := range []int{MaskTest, MaskStopFail} {
		e.SetMaskMode(mode)
		if e.envelopeAllowed(sramcapture.Config{}, edge) {
			t.Fatal("mask testing must preserve sample geometry")
		}
	}
	e.SetMaskMode(MaskOff)
	for _, mode := range []int32{AcqAverage, AcqPrecision, AcqEres} {
		e.acqMode.Store(mode)
		if e.envelopeAllowed(sramcapture.Config{}, edge) {
			t.Fatalf("mode %d needs every sample", mode)
		}
	}
	e.acqMode.Store(AcqPeak)
	if !e.envelopeAllowed(sramcapture.Config{}, edge) {
		t.Fatal("peak mode refused")
	}
	if e.envelopeAllowed(sramcapture.Config{}, trigParams{typ: TrigPulse}) {
		t.Fatal("software pulse qualification needs every sample")
	}
	e.serialMode.Store(SerialTrigger)
	if e.envelopeAllowed(sramcapture.Config{}, edge) {
		t.Fatal("software serial qualification needs every sample")
	}
	if !e.envelopeAllowed(sramcapture.Config{UART: sramcapture.UARTTriggerConfig{Enabled: true}}, edge) {
		t.Fatal("a hardware serial trigger does not need samples")
	}
	e.hardwareEnvelope = false
	if e.envelopeAllowed(sramcapture.Config{}, edge) {
		t.Fatal("an image without the envelope recall")
	}
}
