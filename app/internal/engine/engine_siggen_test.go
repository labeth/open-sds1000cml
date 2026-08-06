package engine

import (
	"testing"

	"open-sds/app/internal/combine"
	"open-sds/app/internal/iface"
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

// TestSiggenArmReassertsRun proves BUG-1's fix: a mid-run SetSiggen toggle actually
// REACHES selRun on the normal capture path. The per-frame arm writes only OP_GO, so
// SetSiggen raises siggenDirty and the bus-owner boundary (serviceCommands) re-asserts
// selRun=runWord() exactly once — carrying RUN[6] to the fabric so the capture record
// shows the synthetic triangle and the host reference-lock can seed.
func TestSiggenArmReassertsRun(t *testing.T) {
	fb := newFakeBus()
	e, _ := newTestEngine(t, fb)

	// A boundary with siggen OFF must NOT write selRun (byte-identical steady state).
	fb.clearWrites()
	e.serviceCommands()
	for _, w := range fb.snapWrites() {
		if w.plane == iface.CS1 && w.sel == iface.SelRUN {
			t.Fatalf("siggen-off serviceCommands wrote selRun (%#04x=%#04x) — not byte-identical", w.sel, w.val)
		}
	}

	// Enable siggen (triangle). The NEXT boundary must re-assert selRun with RUN[6] set.
	e.SetSiggen(true, false)
	fb.clearWrites()
	e.serviceCommands()
	found := false
	for _, w := range fb.snapWrites() {
		if w.plane == iface.CS1 && w.sel == iface.SelRUN {
			found = true
			if w.val&(1<<combine.RunSiggenEnBit) == 0 {
				t.Fatalf("re-asserted selRun %#04x missing siggen enable bit RUN[%d]", w.val, combine.RunSiggenEnBit)
			}
		}
	}
	if !found {
		t.Fatalf("SetSiggen(on) did not re-assert selRun at the frame boundary")
	}

	// The re-assert is a ONE-SHOT: a subsequent boundary with no new toggle must not
	// re-write selRun (the persistent register already holds the siggen bits).
	fb.clearWrites()
	e.serviceCommands()
	for _, w := range fb.snapWrites() {
		if w.plane == iface.CS1 && w.sel == iface.SelRUN {
			t.Fatalf("selRun re-asserted again without a toggle (%#04x) — dirty flag not one-shot", w.val)
		}
	}
}
