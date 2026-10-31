// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"open-sds/app/internal/testenv"
)

// webdecJSCfg is each edge protocol's JS decoder and its settings, the same
// as edgeStreamDecs gives the Go one (the browser's stream decode,
// ADR-STREAM-CLIENT-DECODE, runs these twins).
var webdecJSCfg = map[string]struct {
	fn  string
	cfg map[string]any
}{
	"lin":      {"decodeLIN", map[string]any{"baud": 19200}},
	"dmx512":   {"decodeDMX512", map[string]any{"baud": 250000}},
	"modbus":   {"decodeModbus", map[string]any{"baud": 19200, "parity": "even"}},
	"onewire":  {"decodeOneWire", nil},
	"dht":      {"decodeDHT", nil},
	"cec":      {"decodeCEC", nil},
	"j1850":    {"decodeJ1850", nil},
	"dali":     {"decodeDALI", nil},
	"ps2":      {"decodePS2", nil},
	"wiegand":  {"decodeWiegand", nil},
	"usbpd":    {"decodeUSBPD", map[string]any{"baud": 300000}},
	"ws281x":   {"decodeWS281x", nil},
	"swd":      {"decodeSWD", nil},
	"mdio":     {"decodeMDIO", nil},
	"spdif":    {"decodeSPDIF", nil},
	"quad":     {"decodeQuadrature", nil},
	"stepper":  {"decodeStepper", nil},
	"rcencode": {"decodeRCEncode", map[string]any{"variant": 12}},
	"ook":      {"decodeOOK", nil},
	"morse":    {"decodeMorse", nil},
	"dcf77":    {"decodeDCF77", map[string]any{"baud": 1}},
	"pjdl":     {"decodePJDL", map[string]any{"variant": 1}},
	"dsi":      {"decodeDSI", nil},
	"qi":       {"decodeQi", nil},
	"caliper":  {"decodeCaliper", nil},
	"pwm":      {"decodePWM", nil},
	"miller":   {"decodeMiller", map[string]any{"baud": 106000}},
	"swim":     {"decodeSWIM", map[string]any{"baud": 363636}},
	"sdq":      {"decodeSDQ", map[string]any{"baud": 98425}},
	"sdcard":   {"decodeSDCard", nil},
	"cjtag":    {"decodeCJTAG", nil},
	"maple":    {"decodeMaple", nil},
	"pdi":      {"decodePDI", nil},
}

// webdecCase is one stream for webdec_check.mjs's stream mode.
type webdecCase struct {
	Name      string         `json:"name"`
	Fn        string         `json:"fn"`
	Two       bool           `json:"two"`
	Cfg       map[string]any `json:"cfg"`
	SS        float64        `json:"ss"`
	CloseN    int            `json:"closeN"`
	MaxN      int            `json:"maxN"`
	LeadN     int            `json:"leadN"`
	Subframes bool           `json:"subframes"`
	Words     string         `json:"words"`
	Pieces    [][3]int       `json:"pieces"` // samples from, to, then skipped
	Out       string         `json:"out"`
}

// webdecGoRun decodes the words as a stream over edges in random pieces,
// now and then skipping words that will not arrive, and returns the frames
// and the pieces.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func webdecGoRun(tc streamCPUCase, raw []byte, ss float64, seed int64, edec func(a, b *EdgeLine, s float64) Result) ([]StreamFrame, [][3]int) {
	closeN, maxN, leadN := int(tc.quiet/ss), int((tc.longest+tc.quiet)/ss), int(tc.lead/ss)
	d := NewEdgeStreamDecoder(func(x, y *EdgeLine) Result { return edec(x, y, ss) }, closeN, maxN)
	d.SetLead(leadN)
	d.SetSubframes(tc.c.name == "spdif")
	var a, b EdgeLine
	a.AppendWords(raw, 0)
	b.AppendWords(raw, 1)
	var bl *EdgeLine
	if tc.c.lines == 2 {
		bl = &b
	}
	rng := rand.New(rand.NewSource(seed))
	pushN := max(64, int(min(0.2, max(2*tc.quiet, 0.002))/ss))
	bench := os.Getenv("SDS_WEBDEC_BENCH") != ""
	if bench {
		// the engine's pushes: a fifth of a second (or twice Close), at most 2^18
		pushN = min(int(max(0.2, min(1, 2*tc.quiet))/ss), 1<<18)
	}
	var frames []StreamFrame
	var pieces [][3]int
	lastSkip := 0
	for at := 0; at < a.N; {
		e := min(a.N, at+1+rng.Intn(2*pushN))
		if bench {
			e = min(a.N, at+pushN)
		}
		frames = append(frames, d.PushEdges(&a, bl, at, e)...)
		p := [3]int{at, e, 0}
		if !bench && rng.Intn(50) == 0 && e-lastSkip > 4*maxN {
			lastSkip = e
			k := rng.Intn(pushN)
			d.Skip(k)
			p[2] = k
			e = min(a.N, e+k)
		}
		pieces = append(pieces, p)
		at = e
	}
	return append(frames, d.Flush()...), pieces
}

// webdecFramesJSON is frames as webdec_check.mjs writes them.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func webdecFramesJSON(frames []StreamFrame) string {
	var sb strings.Builder
	sb.WriteString("[")
	for i, f := range frames {
		if i > 0 {
			sb.WriteString(",")
		}
		fmt.Fprintf(&sb, "[%d,%d,[", f.I0, f.I1)
		for k, s := range f.Spans {
			if k > 0 {
				sb.WriteString(",")
			}
			t, _ := json.Marshal(s.Text)
			fmt.Fprintf(&sb, "[%d,%d,%q,%d,%s]", s.I0, s.I1, s.Kind, s.Val, t)
		}
		sb.WriteString("]]")
	}
	sb.WriteString("]")
	return sb.String()
}

// The browser's stream decode (webdec.js: EdgeLine, the stream decoder over
// edges and the JS decoders) hands out exactly the Go stream decoder's
// frames - every span's samples, kind, value and text - for every protocol
// with an edge decoder, on long random traffic, dense and bursty, pushed in
// the same random pieces with lost stretches between.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func TestWebDecStreamParity(t *testing.T) {
	testenv.NeedNode(t)
	names := make([]string, 0, len(edgeStreamDecs))
	for n := range edgeStreamDecs {
		names = append(names, n)
	}
	sort.Strings(names)
	if only := os.Getenv("SDS_WEBDEC_ONLY"); only != "" {
		names = strings.Split(only, ",")
	}
	budget := 1.5e6 // samples a run
	if testing.Short() {
		budget = 2e5
	}
	dir := t.TempDir()
	if keep := os.Getenv("SDS_WEBDEC_KEEP"); keep != "" {
		dir = keep // the cases stay for a profile (node --cpu-prof webdec_check.mjs stream <dir>/cases.json)
	}
	var cases []webdecCase
	want := map[string][]StreamFrame{}
	for _, name := range names {
		js, ok := webdecJSCfg[name]
		if !ok {
			t.Errorf("%s: no JS decoder for the browser's stream", name)
			continue
		}
		tc := streamCPUCaseOf(t, name)
		ss := edgeStreamPeriod(tc)
		secs := min(max(streamGoldenSecs(tc), 40*tc.longest), budget*ss)
		if v, err := strconv.ParseFloat(os.Getenv("SDS_WEBDEC_SECS"), 64); err == nil && v > 0 {
			secs = v // a bench's length (with SDS_WEBDEC_BENCH)
		}
		for k, bursty := range []bool{false, true} {
			seed := int64(len(name))*37 + int64(k)
			la, lb := streamBitsLines(tc, seed, secs, ss, bursty)
			raw := levelWords(la, lb)
			frames, pieces := webdecGoRun(tc, raw, ss, seed, edgeStreamDecs[name])
			if len(frames) == 0 && secs >= 3*(tc.longest+tc.quiet) {
				t.Errorf("%s bursty %v: no frames", name, bursty)
			}
			id := fmt.Sprintf("%s-%v", name, bursty)
			wf := filepath.Join(dir, id+".words")
			if err := os.WriteFile(wf, raw, 0o644); err != nil {
				t.Fatal(err)
			}
			cfg := map[string]any{"threshold": 128}
			if tc.c.lines == 2 {
				cfg["thresholdB"] = 128
			}
			for k, v := range js.cfg {
				cfg[k] = v
			}
			cases = append(cases, webdecCase{Name: id, Fn: js.fn, Two: tc.c.lines == 2, Cfg: cfg, SS: ss,
				CloseN: int(tc.quiet / ss), MaxN: int((tc.longest + tc.quiet) / ss), LeadN: int(tc.lead / ss), Subframes: name == "spdif",
				Words: wf, Pieces: pieces, Out: filepath.Join(dir, id+".json")})
			want[id] = frames
		}
	}
	cj := filepath.Join(dir, "cases.json")
	blob, _ := json.Marshal(cases)
	if err := os.WriteFile(cj, blob, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("node", "../web/webdec_check.mjs", "stream", cj).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "WEBDEC OK") {
		t.Fatalf("webdec_check.mjs: %v\n%s", err, out)
	}
	for _, c := range cases {
		b, err := os.ReadFile(c.Out)
		if err != nil {
			t.Fatal(err)
		}
		var got struct {
			Ms     float64           `json:"ms"`
			Edges  int               `json:"edges"`
			Frames []json.RawMessage `json:"frames"`
		}
		if err := json.Unmarshal(b, &got); err != nil {
			t.Fatal(err)
		}
		var gj strings.Builder
		gj.WriteString("[")
		for i, f := range got.Frames {
			if i > 0 {
				gj.WriteString(",")
			}
			gj.Write(f)
		}
		gj.WriteString("]")
		var g, w any
		_ = json.Unmarshal([]byte(gj.String()), &g)
		_ = json.Unmarshal([]byte(webdecFramesJSON(want[c.Name])), &w)
		if !reflect.DeepEqual(g, w) {
			gf, _ := g.([]any)
			wf, _ := w.([]any)
			first := ""
			for i := 0; i < min(len(gf), len(wf)); i++ {
				if !reflect.DeepEqual(gf[i], wf[i]) {
					first = fmt.Sprintf("frame %d: js %v, go %v", i, gf[i], wf[i])
					break
				}
			}
			t.Errorf("%s: %d frames in the browser, %d in Go; %s", c.Name, len(gf), len(wf), first)
			continue
		}
		sig := float64(c.Pieces[len(c.Pieces)-1][1]) * c.SS
		t.Logf("%-16s %4d frames alike; %.3g us period; browser decode %6.0f ms for %7d edges, %6.3g s of signal: %6.3g x real time, %6.3g M edges/s",
			c.Name, len(want[c.Name]), c.SS*1e6, got.Ms, got.Edges, sig, sig/(got.Ms/1e3), float64(got.Edges)/got.Ms/1e3)
	}
}
