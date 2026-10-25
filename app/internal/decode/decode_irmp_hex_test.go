// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"fmt"
	"testing"
)

// labelHex reads as fmt's "%s%X", negative values included.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-255
func TestLabelHexMatchesFmt(t *testing.T) {
	for _, v := range []int{0, 1, 0xa, 0xff, 0x1fff, 0xabcdef, -1, -0x2b, 1<<31 - 1} {
		for _, l := range []string{"", "A", "@", "CMD "} {
			if got, want := labelHex(l, v), fmt.Sprintf("%s%X", l, v); got != want {
				t.Errorf("labelHex(%q, %d) = %q, want %q", l, v, got, want)
			}
		}
	}
}

// A Denon pair's length moves with its bits: the longest (every bit a 1)
// still lies within MessageMaxS, which only the pair protocol names.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-255
func TestDenonMessageMax(t *testing.T) {
	const sr = 200000
	w := irOracleLine(sr, func(l irLine) {
		irDenon(l, 0x1F, 0x3FF)
		l.space(100e-3)
	})
	r := DecodeIRMP(w.codes(), 1.0/sr, LineCfg{})
	if r.FrameBits != IRDenon {
		t.Fatalf("decoded %d: %s", r.FrameBits, r.Error)
	}
	lo, hi := r.Spans[0].I0, r.Spans[len(r.Spans)-1].I1
	if got, bound := float64(hi-lo)/sr, MessageMaxS(r); !(bound > got) || got < 80e-3 {
		t.Fatalf("pair of %g s, bound %g s", got, bound)
	}
	if MessageMaxS(Result{Proto: "irmp", FrameBits: IRSamsung32}) != 0 || MessageMaxS(Result{Proto: "uart"}) != 0 {
		t.Fatal("a bound for a protocol that does not fix its length")
	}
}
