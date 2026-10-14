// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import "testing"

// The byte picker offers whole units of the buses whose matcher takes words:
// an ARINC 429 field is three of the BUS table's bytes, a MIL-1553 word two.
// "Trig on 2" on the ARINC words 00 AB CD / 01 23 45 offered "00 AB" (read as
// the field 0xAB: nothing triggered); an ARINC match is one whole word (each
// word is a frame of its own), a MIL-1553 one up to four words.
// TRLC-LINKS: REQ-SDS-214
func TestPickerOffersWholeWords(t *testing.T) {
	c, _, _ := newC(t)
	c.mu.Lock()
	c.ui.pick = pickFrom([][]string{{"00", "AB", "CD"}, {"01", "23", "45"}}, []bool{true, true}, 0, pickUnitOf("arinc429"))
	c.mu.Unlock()
	keys := c.pickKeys(c.snap())
	if keys[0].value != "00 AB CD" || keys[0].dim || !keys[1].dim || !keys[2].dim {
		t.Fatalf("ARINC picker keys %q (dim %v), 2 words dim %v: one word only", keys[0].value, keys[0].dim, keys[1].dim)
	}
	c.pickMove(1)
	if keys := c.pickKeys(c.snap()); keys[0].value != "01 23 45" {
		t.Fatalf("Adjust moves a word: first key %q, want 01 23 45", keys[0].value)
	}
	c.mu.Lock()
	c.ui.pick = pickFrom([][]string{{"A5", "5A"}, {"5A", "A5"}}, []bool{true, true}, 0, pickUnitOf("mil1553"))
	c.mu.Unlock()
	if keys := c.pickKeys(c.snap()); keys[0].value != "A5 5A" || keys[1].value != "A5 5A 5A A5" {
		t.Fatalf("MIL-1553 picker keys %q %q", keys[0].value, keys[1].value)
	}
	if pickUnitOf("uart") != 1 {
		t.Fatal("a UART is picked byte by byte")
	}
}
