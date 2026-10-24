// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"math/rand"
	"slices"
	"strings"
	"testing"
)

// wrapTextRef is wrapText word by word, the reference its fast path keeps.
// TRLC-LINKS: REQ-SDS-212
func wrapTextRef(s string, n int) []string {
	var out []string
	line := ""
	for _, w := range strings.Fields(s) {
		for len(w) > n {
			if line != "" {
				out = append(out, line)
				line = ""
			}
			out = append(out, w[:n])
			w = w[n:]
		}
		switch {
		case line == "":
			line = w
		case len(line)+1+len(w) <= n:
			line += " " + w
		default:
			out = append(out, line)
			line = w
		}
	}
	if line != "" {
		out = append(out, line)
	}
	return out
}

// wrapText reads as the word-by-word reference on random texts, long
// words, odd spacing and non-ASCII included.
// TRLC-LINKS: REQ-SDS-212
func TestWrapTextMatchesRef(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	alpha := []string{"a", "bb", "ccc", "dddddddddd", "eeeeeeeeeeeeeeeeeeeeeeeee", " ", " ", "  ", "\t", "é", "x"}
	for i := 0; i < 20000; i++ {
		var b strings.Builder
		for k := rng.Intn(30); k > 0; k-- {
			b.WriteString(alpha[rng.Intn(len(alpha))])
		}
		s := b.String()
		for _, n := range []int{1, 3, 8, 20, 40} {
			if got, want := wrapText(s, n), wrapTextRef(s, n); !slices.Equal(got, want) {
				t.Fatalf("wrapText(%q, %d) = %q, want %q", s, n, got, want)
			}
		}
	}
}
