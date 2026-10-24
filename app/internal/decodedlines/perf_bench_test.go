// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package decodedlines

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// formatRef is Format as written with fmt: the oracle the faster one keeps.
// TRLC-LINKS: REQ-SDS-018
func formatRef(t *Transcript, i int) string {
	l := t.At(i)
	g1, g2 := byte(' '), byte(' ')
	ms := float64(l.Sample-t.first) * 2e-6
	if t.marked {
		if i == t.TriggerLine() {
			g1 = '>'
		}
		if l.Sample >= t.recFirst && l.Sample < t.recLast {
			g2 = '|'
		}
		ms = (float64(l.Sample) - float64(t.trig)) * 2e-6
	}
	var hex, text strings.Builder
	for k := 0; k < int(l.N); k++ {
		if k > 0 {
			hex.WriteByte(' ')
		}
		if l.Errs&(1<<k) != 0 {
			hex.WriteString(strings.Repeat("!", t.hexDigits))
			text.WriteByte('!')
			continue
		}
		fmt.Fprintf(&hex, "%0*X", t.hexDigits, l.Vals[k])
		if l.Naks&(1<<k) != 0 {
			text.WriteByte('~')
		} else if b := l.Vals[k]; b >= 0x20 && b < 0x7f {
			text.WriteByte(byte(b))
		} else {
			text.WriteByte('.')
		}
	}
	w := t.perLine*(t.hexDigits+1) - 1
	s := fmt.Sprintf("%c%c%+11.4f %-6s %-*s", g1, g2, ms, l.Note, w, hex.String())
	if t.ascii {
		s += "  " + text.String()
	}
	return s
}

// benchTranscript holds n random lines of proto.
// TRLC-LINKS: REQ-SDS-018
func benchTranscript(proto, n int, r *rand.Rand) *Transcript {
	t := New(proto, n)
	notes := []string{"", "W 50", "GAP", "SOP\"D", "LOST 12", "SOF", "µs"}
	s := uint64(1000)
	for i := 0; i < n; i++ {
		var l Line
		l.Sample = s
		s += uint64(r.Intn(100000))
		l.N = uint8(1 + r.Intn(16))
		for k := range l.Vals {
			switch r.Intn(4) {
			case 0:
				l.Vals[k] = uint32(r.Intn(256))
			case 1:
				l.Vals[k] = uint32(0x20 + r.Intn(0x5f))
			case 2:
				l.Vals[k] = r.Uint32()
			default:
				l.Vals[k] = uint32(r.Intn(16))
			}
		}
		l.Errs = uint16(r.Intn(1<<16)) & uint16(r.Intn(1<<16)) & uint16(r.Intn(1<<16))
		l.Naks = uint16(r.Intn(1<<16)) & uint16(r.Intn(1<<16))
		l.Note = notes[r.Intn(len(notes))]
		t.Append(i, l)
	}
	return t
}

// Format renders exactly as the fmt version did, for every protocol layout,
// marked or not.
// TRLC-LINKS: REQ-SDS-018
func TestFormatIsTheReference(t *testing.T) {
	r := rand.New(rand.NewSource(5))
	for _, proto := range []int{0, 5, 7, 8, 26, 28, 29, 31, 52} {
		tr := benchTranscript(proto, 300, r)
		for pass := 0; pass < 2; pass++ {
			for i := 0; i < tr.Count(); i++ {
				if got, want := tr.Format(i), formatRef(tr, i); got != want {
					t.Fatalf("proto %d line %d:\n got %q\nwant %q", proto, i, got, want)
				}
			}
			tr.Mark(tr.At(150).Sample+3, tr.At(40).Sample, tr.At(200).Sample)
		}
	}
}

// TRLC-LINKS: REQ-SDS-018
func BenchmarkFormat(b *testing.B) {
	tr := benchTranscript(0, 256, rand.New(rand.NewSource(1)))
	i := 0
	for b.Loop() {
		_ = tr.Format(i & 255)
		i++
	}
}
