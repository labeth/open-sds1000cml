package engine

import (
	"testing"

	"open-sds/app/internal/combine"
)

// TestSiggenRunWordBits proves the fabric FAST-SIGNAL GENERATOR hook is additive and
// OFF-by-default byte-identical: SetSiggen only OR-sets the previously-FREE RUN[6]
// (enable) / RUN[7] (shape) bits into the composed run word, and clearing it returns
// the run word bit-for-bit to today's value.
func TestSiggenRunWordBits(t *testing.T) {
	fb := newFakeBus()
	e, _ := newTestEngine(t, fb)

	base := e.runWord() // baseline: siggen off (default) => today's mode+run word

	// enable bit must NOT be set at reset (byte-identical invariant).
	if base&(1<<combine.RunSiggenEnBit) != 0 {
		t.Fatalf("default runWord %#04x already has siggen enable bit set", base)
	}

	// triangle: enable only.
	e.SetSiggen(true, false)
	if got, want := e.runWord(), base|(1<<combine.RunSiggenEnBit); got != want {
		t.Fatalf("siggen triangle runWord = %#04x, want %#04x", got, want)
	}

	// ramp: enable + shape.
	e.SetSiggen(true, true)
	if got, want := e.runWord(), base|(1<<combine.RunSiggenEnBit)|(1<<combine.RunSiggenShapeBit); got != want {
		t.Fatalf("siggen ramp runWord = %#04x, want %#04x", got, want)
	}

	// shape must not leak when the generator is disabled.
	e.SetSiggen(false, true)
	if got := e.runWord(); got != base {
		t.Fatalf("siggen off runWord = %#04x, want byte-identical baseline %#04x", got, base)
	}
}
