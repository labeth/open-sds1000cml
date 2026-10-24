// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package streamview

import (
	"testing"

	"open-sds/app/internal/decodedlines"
)

// benchController holds n 16-unit lines.
// TRLC-LINKS: REQ-SDS-018
func benchController(n int) *Controller {
	t := decodedlines.New(1, n)
	for i := 0; i < n; i++ {
		l := decodedlines.Line{Sample: uint64(1000 * i), N: 16}
		for k := range l.Vals {
			l.Vals[k] = uint32(i + k)
		}
		t.Append(i, l)
	}
	return &Controller{t: t}
}

// TRLC-LINKS: REQ-SDS-018
func BenchmarkLines(b *testing.B) {
	c := benchController(4096)
	for b.Loop() {
		c.Lines(1000, 500)
	}
}

// TRLC-LINKS: REQ-SDS-018
func BenchmarkWindow(b *testing.B) {
	c := benchController(4096)
	for b.Loop() {
		c.Window(ListRows, 100)
	}
}

// Lines gives each line its own units, and a line without any none.
// TRLC-LINKS: REQ-SDS-018
func TestLinesUnitsAreTheLines(t *testing.T) {
	c := benchController(64)
	c.t.Append(70, decodedlines.Line{Sample: 99999, N: 2, Vals: [16]uint32{7, 8}})
	p := c.Lines(60, 100)
	for _, l := range p.Lines {
		h := c.t.At(l.Index - c.t.Base())
		if len(l.Units) != int(h.N) || (h.N == 0) != (l.Units == nil) {
			t.Fatalf("line %d: %v, want %d units", l.Index, l.Units, h.N)
		}
		for k, v := range l.Units {
			if v != h.Vals[k] {
				t.Fatalf("line %d unit %d: %d, want %d", l.Index, k, v, h.Vals[k])
			}
		}
	}
	p.Lines[0].Units = append(p.Lines[0].Units, 1)
	if p.Lines[1].Units[0] != c.t.At(p.Lines[1].Index - c.t.Base()).Vals[0] {
		t.Fatal("a line's units grew into the next line's")
	}
}
