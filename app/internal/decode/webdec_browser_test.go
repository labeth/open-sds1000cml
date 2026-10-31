// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"open-sds/app/internal/testenv"
)

// webdecRLE codes words as the engine's ring does (engine appendWordsRLE,
// which engine/soft_client_test.go checks against the page's reader): a
// word k < 2^31 then k words, or 2^31|k then a quiet word that comes k
// times.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func webdecRLE(raw []byte, mask uint32) []byte {
	n := len(raw) / 4
	var out []byte
	lit := -1
	for i := 0; i < n; {
		w := binary.LittleEndian.Uint32(raw[4*i:]) & mask
		lo, hi := uint16(w), uint16(w>>16)
		if (lo == 0 || lo == 0xFFFF) && (hi == 0 || hi == 0xFFFF) {
			j := i + 1
			for j < n && binary.LittleEndian.Uint32(raw[4*j:])&mask == w {
				j++
			}
			if j-i >= 3 {
				lit = -1
				out = binary.LittleEndian.AppendUint32(out, 1<<31|uint32(j-i))
				out = binary.LittleEndian.AppendUint32(out, w)
				i = j
				continue
			}
		}
		if lit < 0 {
			lit = len(out)
			out = binary.LittleEndian.AppendUint32(out, 0)
		}
		binary.LittleEndian.PutUint32(out[lit:], binary.LittleEndian.Uint32(out[lit:])+1)
		out = binary.LittleEndian.AppendUint32(out, w)
		i++
	}
	return out
}

// webdecBenchCase is one stream the bench server plays.
type webdecBenchCase struct {
	name   string
	info   map[string]any
	chunks [][]byte // each chunk's words, coded
	words  []int
	secs   float64 // signal time
	edges  int
	// filled as the worker reads and posts
	start, end time.Time
	frames     int
	losses     int
}

// webdecBenchInfo is the stream header the engine sends for a case
// (engine ClientStreamInfo) with the settings its registry would use.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func webdecBenchInfo(tc streamCPUCase, ss float64, session int) map[string]any {
	p := map[string]any{"chA": 0, "threshold": 128, "haveThr": true, "thresholdB": 128, "haveThrB": true}
	switch tc.c.name {
	case "lin", "dmx512", "modbus", "usbpd":
		p["baud"] = int(tc.c.baud)
	}
	if tc.c.name == "modbus" {
		p["parity"] = "even"
	}
	lead := int(tc.lead / ss)
	leadWords := lead/16 + 2
	mask := uint32(0xFFFF)
	if tc.c.lines == 2 {
		mask = 0xFFFFFFFF
	}
	return map[string]any{"session": session, "proto": tc.c.name, "params": p, "periodS": ss, "lines": tc.c.lines,
		"closeN": int(tc.quiet / ss), "maxN": int((tc.longest + tc.quiet) / ss), "leadN": lead,
		"pushN": min(int(math.Max(0.2, math.Min(1, 2*tc.quiet))/ss), 1<<18), "pushMax": 1 << 18,
		"keepWords": int(tc.quiet/ss)/16 + leadWords + 2, "leadWords": leadWords, "subframes": tc.c.name == "spdif",
		"mask": mask, "active": true}
}

// TestWebDecBrowserThroughput plays dense and bursty digital streams of
// the fast buses to the page's decode worker (webdec_worker.js) in headless
// Chromium, as fast as it reads them, and reports what the browser decodes
// a second - words, edges and the share of real time - beside the network
// bytes each stream takes with and without the ring's quiet-line coding.
// SDS_WEBDEC_SECS sets each stream's signal time (1 s; 0.2 s with -short),
// SDS_WEBDEC_ONLY the protocols.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func TestWebDecBrowserThroughput(t *testing.T) {
	testenv.NeedNode(t)
	names := []string{"maple", "mdio", "swd", "ws281x", "swim", "spdif", "sdcard", "pdi", "dmx512", "onewire", "usbpd", "lin"}
	if only := os.Getenv("SDS_WEBDEC_ONLY"); only != "" {
		names = strings.Split(only, ",")
	}
	secs := 1.0
	if testing.Short() {
		secs = 0.2
	}
	if v, err := strconv.ParseFloat(os.Getenv("SDS_WEBDEC_SECS"), 64); err == nil && v > 0 {
		secs = v
	}
	var cases []*webdecBenchCase
	for _, name := range names {
		tc := streamCPUCaseOf(t, name)
		ss := edgeStreamPeriod(tc)
		for _, bursty := range []bool{false, true} {
			la, lb := streamBitsLines(tc, 5, max(secs, 3*(tc.longest+tc.quiet)), ss, bursty)
			raw := levelWords(la, lb)
			info := webdecBenchInfo(tc, ss, len(cases)+1)
			mask := info["mask"].(uint32)
			c := &webdecBenchCase{name: fmt.Sprintf("%s %s", name, map[bool]string{false: "dense", true: "bursty"}[bursty]), info: info,
				secs: float64(len(raw)/4*16) * ss}
			for at := 0; at < len(raw); at += 65536 {
				piece := raw[at:min(len(raw), at+65536)]
				c.chunks = append(c.chunks, webdecRLE(piece, mask))
				c.words = append(c.words, len(piece)/4)
			}
			for ch := 0; ch < tc.c.lines; ch++ {
				var l EdgeLine
				l.AppendWords(raw, ch)
				c.edges += len(l.At)
			}
			cases = append(cases, c)
		}
	}

	var mu sync.Mutex
	cur, next := 0, 0 // the case played and its next chunk
	webDir, _ := filepath.Abs("../web")
	files := http.FileServer(http.Dir(webDir))
	mux := http.NewServeMux()
	mux.HandleFunc("/bench.html", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, `<!doctype html><title>webdec bench</title><script>
window.benchStats = {}; window.benchErrors = [];
const w = new Worker("webdec_worker.js");
w.onmessage = (ev) => { const s = ev.data; if (s && s.type === "status" && s.session) window.benchStats[s.session] = s; };
w.onerror = (e) => window.benchErrors.push(e.message);
w.postMessage({ cmd: "start" });
</script>`)
	})
	mux.HandleFunc("/bench/state", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if cur >= len(cases) {
			io.WriteString(w, "done")
			return
		}
		io.WriteString(w, "running")
	})
	mux.HandleFunc("/api/stream/words.bin", func(w http.ResponseWriter, r *http.Request) {
		from, _ := strconv.ParseUint(r.URL.Query().Get("from"), 10, 64)
		maxB, _ := strconv.Atoi(r.URL.Query().Get("max"))
		mu.Lock()
		now := time.Now()
		if cur < len(cases) && next >= len(cases[cur].chunks) {
			// the worker asks past the stream's end: it has decoded all of it
			cases[cur].end = now
			cur, next = cur+1, 0
		}
		var hdr map[string]any
		var heads, body []byte
		seq := from
		if cur < len(cases) {
			c := cases[cur]
			if next == 0 {
				c.start = now
			}
			hdr = map[string]any{}
			for k, v := range c.info {
				hdr[k] = v
			}
			index := uint64(0)
			for k := 0; k < next; k++ {
				index += uint64(16 * c.words[k])
			}
			n := 0
			for next < len(c.chunks) && (n == 0 || len(body)+len(c.chunks[next]) <= maxB) {
				for _, v := range []uint32{uint32(index), uint32(index >> 32), uint32(c.words[next]), 2, uint32(len(c.chunks[next]))} {
					heads = binary.LittleEndian.AppendUint32(heads, v)
				}
				body = append(body, c.chunks[next]...)
				index += uint64(16 * c.words[next])
				next++
				n++
				seq++
			}
			hdr["chunks"] = n
		} else {
			hdr = map[string]any{"active": false, "why": "played", "chunks": 0}
		}
		hdr["next"] = seq
		mu.Unlock()
		if cur >= len(cases) {
			time.Sleep(200 * time.Millisecond)
		}
		hj, _ := json.Marshal(hdr)
		for len(hj)%4 != 0 {
			hj = append(hj, ' ')
		}
		out := append([]byte("SDW1"), 0, 0, 0, 0)
		binary.LittleEndian.PutUint32(out[4:], uint32(len(hj)))
		out = append(out, hj...)
		out = append(out, heads...)
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write(append(out, body...))
	})
	mux.HandleFunc("/api/stream/frames.bin", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if len(b) < 20 || string(b[:4]) != "SDF1" {
			http.Error(w, "bad frames", 400)
			return
		}
		session := int(binary.LittleEndian.Uint32(b[4:]))
		n := int(binary.LittleEndian.Uint32(b[16:]))
		frames, losses := 0, 0
		at := 20
		for range n {
			ns, tl := int(binary.LittleEndian.Uint16(b[at+16:])), int(b[at+18])
			at += 20 + tl
			if ns == 1 && b[at+12] == 6 {
				losses++
			} else {
				frames++
			}
			at += 13 * ns
		}
		mu.Lock()
		if session >= 1 && session <= len(cases) {
			cases[session-1].frames += frames
			cases[session-1].losses += losses
		}
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.Handle("/", files)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	out, err := exec.Command("node", "../web/webdec_browser.mjs", srv.URL).CombinedOutput()
	if strings.Contains(string(out), "SKIP:") {
		testenv.SkipBrowser(t, "browser driver skipped: %s", strings.SplitN(string(out), "\n", 2)[0])
	}
	if err != nil || !strings.Contains(string(out), "ALL PASS") {
		t.Fatalf("webdec_browser.mjs: %v\n%s", err, out)
	}
	stats := map[int]map[string]any{}
	for _, line := range strings.Split(string(out), "\n") {
		if s, ok := strings.CutPrefix(line, "STAT "); ok {
			var m map[string]any
			if json.Unmarshal([]byte(s), &m) == nil {
				stats[int(m["session"].(float64))] = m
			}
		}
	}
	var sb strings.Builder
	p := func(f string, a ...any) { fmt.Fprintf(&sb, f, a...) }
	p("headless Chromium web worker, each stream played as fast as the worker reads it\n")
	p("%-16s %8s %10s %10s | %9s %9s | %9s %8s %7s | %7s %6s\n", "stream", "period", "words/s", "edges/s", "raw B/s", "coded B/s",
		"dec Mw/s", "dec Me/s", "x real", "frames", "lost")
	for i, c := range cases {
		words, coded := 0, 0
		for k := range c.chunks {
			words += c.words[k]
			coded += len(c.chunks[k])
		}
		period := c.info["periodS"].(float64)
		st := stats[i+1]
		busy := 0.0
		if st != nil {
			busy = st["busyMs"].(float64) / 1e3
		}
		wall := c.end.Sub(c.start).Seconds()
		if c.end.IsZero() || busy <= 0 {
			t.Errorf("%s: not played (busy %v)", c.name, busy)
			continue
		}
		p("%-16s %6.3gus %10.3g %10.3g | %9.3g %9.3g | %9.3g %8.3g %7.2f | %7d %6d\n", c.name, period*1e6, float64(words)/c.secs,
			float64(c.edges)/c.secs, float64(4*words)/c.secs, float64(coded)/c.secs, float64(words)/busy/1e6, float64(c.edges)/busy/1e6,
			c.secs/busy, c.frames, c.losses)
		_ = wall
		pushS := float64(c.info["pushN"].(int)+c.info["closeN"].(int)) * period
		if c.frames == 0 && c.secs > 2*pushS {
			t.Errorf("%s: the worker posted no frames", c.name)
		}
		if c.losses != 0 {
			t.Errorf("%s: the worker lost words", c.name)
		}
	}
	t.Log("\n" + sb.String())
	if path := os.Getenv("SDS_WEBDEC_OUT"); path != "" {
		_ = os.WriteFile(path, []byte(sb.String()), 0o644)
	}
}
