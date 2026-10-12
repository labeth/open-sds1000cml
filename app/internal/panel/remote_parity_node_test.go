// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"open-sds/app/internal/panel/wsview"
	"open-sds/app/internal/testenv"
)

// TestRemoteCoreParity: what the phone remote says before it sends a value
// (remote_core.js) is what the scope says: si at three figures, every number
// row's own format over its range and steps, typed numbers with their errors,
// the refusal of a value out of range, and match bytes in hex and text.
// Skips without node (fails under CI_REQUIRE_BROWSER=1).
// TRLC-LINKS: REQ-SDS-212, REQ-SDS-220
func TestRemoteCoreParity(t *testing.T) {
	testenv.NeedNode(t)
	node, _ := exec.LookPath("node")
	c, _, _ := newC(t)
	s := c.snap()

	type siVec struct {
		V    float64 `json:"v"`
		Unit string  `json:"unit"`
	}
	type fmtVec struct {
		Row RowModel `json:"row"`
		V   float64  `json:"v"`
	}
	type parseVec struct {
		Text string `json:"text"`
		Unit string `json:"unit"`
	}
	type rangeVec struct {
		Row  RowModel `json:"row"`
		Text string   `json:"text"`
	}
	var vec struct {
		SI    []siVec    `json:"si"`
		Fmt   []fmtVec   `json:"fmt"`
		Parse []parseVec `json:"parse"`
		Range []rangeVec `json:"range"`
		Match []string   `json:"match"`
	}
	var wantSI, wantFmt, wantRange []string
	for _, u := range []string{"V", "s", "Hz", "b/s"} {
		for _, v := range []float64{0, 1, -1, 1.25, 0.5, 999.4, 999.6, 1e-3, 1.2345e-3, 2e-9, 5e-7, 43.9078e-3, -0.02, 115200, 1.5e6, 20e6, 1e-12, 3.3e-13} {
			vec.SI = append(vec.SI, siVec{v, u})
			wantSI = append(wantSI, si(v, u))
		}
	}
	// every number row's format over its range, its value and its steps
	var numbers []*row
	for _, tab := range sheetTabs {
		for _, r := range tab.rows {
			if r.kind == wsview.Number {
				numbers = append(numbers, r)
			}
		}
	}
	for _, r := range numbers {
		m := rowModel(s, r)
		lo, hi := r.lim(s)
		vals := []float64{lo, hi, r.val(s)}
		if st := r.step(s); st > 0 {
			for k := -3.0; k <= 3; k++ {
				if v := r.val(s) + k*st; v >= lo && v <= hi {
					vals = append(vals, v)
				}
			}
		}
		if r.ladder != nil {
			vals = append(vals, r.ladder(s)...)
		}
		for _, v := range vals {
			vec.Fmt = append(vec.Fmt, fmtVec{m, v})
			wantFmt = append(wantFmt, r.fmtN(v))
		}
		// a value out of range, above or below: refused with the same words,
		// nothing clamped (a negative one written as itself: -1s, -0x01)
		for _, out := range []float64{hi + math.Max(math.Abs(hi), 1), lo - math.Max(math.Abs(lo), 1)} {
			vec.Range = append(vec.Range, rangeVec{m, strconvG(out)})
			err := r.setNumber(c, s, out)
			if err == nil {
				t.Fatalf("%s: %g outside its range was not refused", r.id, out)
			}
			wantRange = append(wantRange, err.Error())
		}
	}
	for _, unit := range []string{"V", "s", "%", "div", "b/s", ""} {
		for _, text := range []string{"1.25", "1.25V", "1.25 V", "500u", "500us", "500 us", "-20 mV", "+1.2", "1e-3", "2k", "115.2k", "50%", "25 %",
			"0x1F", "-0x10", "0xZZ", "abc", "", "  ", "1.2.3", "1e", "1e5", "5mA", "2 volts", "3 sec", "10 div", "9600 baud", ".5", "-.5ms", "1Meg", "µs", "3µs",
			"2,5V", "2,5", "115,2k", "57,600", "1,2,3", "1.2,3", ",5", "-0,5 ms", "1,5e3", "0x1,2", "2,5 volts", "0,125", "1,250 ms",
			"-1,500", "1,2345", "12,34k", "+3,000V", "000,500"} {
			vec.Parse = append(vec.Parse, parseVec{text, unit})
		}
	}
	vec.Match = []string{"55 AA 0F", "55aa", "55,AA", "?? A? ?F ERR", "5", "55 ZZ", "", "any", "Any Byte", `"Hi"`, `"Hi\r\n"`, `'\x41\\'`, `"\q"`, `""`,
		`"\x4"`, `"a\"b"`, strings.Repeat("00 ", 33), strings.Repeat("A", 64), `"é"`, `"tab\there"`, "AAB", "err ??"}

	dir := t.TempDir()
	blob, err := json.Marshal(vec)
	if err != nil {
		t.Fatal(err)
	}
	vf := filepath.Join(dir, "vectors.json")
	if err := os.WriteFile(vf, blob, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{filepath.Join("testdata", "remoteparity.cjs"), filepath.Join("..", "web", "remote_core.js")} {
		if _, err := os.ReadFile(f); err != nil { // read here so Go's test cache sees a change to them (node reads them)
			t.Fatal(err)
		}
	}
	cmd := exec.Command(node, filepath.Join("testdata", "remoteparity.cjs"), vf, filepath.Join("..", "web"))
	cmd.Stderr = os.Stderr
	raw, err := cmd.Output()
	if err != nil {
		t.Fatalf("remote_core.js run: %v", err)
	}
	var got struct {
		SI    []string `json:"si"`
		Fmt   []string `json:"fmt"`
		Parse []struct {
			OK    bool    `json:"ok"`
			Value float64 `json:"value"`
			Error string  `json:"error"`
		} `json:"parse"`
		Range []string `json:"range"`
		Match []struct {
			OK     bool     `json:"ok"`
			Tokens []string `json:"tokens"`
			Error  string   `json:"error"`
		} `json:"match"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	for i, w := range wantSI {
		if got.SI[i] != w {
			t.Errorf("si(%g, %q): Go %q, JS %q", vec.SI[i].V, vec.SI[i].Unit, w, got.SI[i])
		}
	}
	for i, w := range wantFmt {
		if got.Fmt[i] != w {
			t.Errorf("%s at %g: Go %q, JS %q", vec.Fmt[i].Row.ID, vec.Fmt[i].V, w, got.Fmt[i])
		}
	}
	for i, p := range vec.Parse {
		v, err := parseNumber(p.Text, p.Unit)
		g := got.Parse[i]
		switch {
		case (err == nil) != g.OK:
			t.Errorf("parseNumber(%q, %q): Go %v %v, JS ok=%v %q", p.Text, p.Unit, v, err, g.OK, g.Error)
		case err == nil && math.Abs(v-g.Value) > 1e-12*math.Max(1, math.Abs(v)):
			t.Errorf("parseNumber(%q, %q): Go %g, JS %g", p.Text, p.Unit, v, g.Value)
		case err != nil && unquote(err.Error()) != unquote(g.Error):
			t.Errorf("parseNumber(%q, %q): Go %q, JS %q", p.Text, p.Unit, err.Error(), g.Error)
		}
	}
	for i, w := range wantRange {
		if got.Range[i] != w {
			t.Errorf("%s out of range: Go %q, JS %q", vec.Range[i].Row.ID, w, got.Range[i])
		}
	}
	for i, text := range vec.Match {
		toks, err := parseMatchText(text)
		g := got.Match[i]
		switch {
		case (err == nil) != g.OK:
			t.Errorf("match %q: Go %v %v, JS ok=%v %q", text, toks, err, g.OK, g.Error)
		case err == nil && strings.Join(toks, " ") != strings.Join(g.Tokens, " "):
			t.Errorf("match %q: Go %v, JS %v", text, toks, g.Tokens)
		case err != nil && err.Error() != g.Error:
			t.Errorf("match %q: Go %q, JS %q", text, err.Error(), g.Error)
		}
	}
	t.Logf("%d si, %d formats over %d number rows, %d typed numbers, %d range refusals, %d match texts", len(wantSI), len(wantFmt), len(numbers),
		len(vec.Parse), len(wantRange), len(vec.Match))
}

// strconvG writes v so the parsers read it back exactly.
func strconvG(v float64) string { return fmt.Sprintf("%.10g", v) }

// unquote folds the two languages' quoting of the typed text in an error
// (Go %q escapes, JS writes the text as typed).
func unquote(s string) string {
	return strings.NewReplacer(`\"`, `"`, `µ`, "µ", `\t`, "\t").Replace(s)
}
