// ENGMODEL-OWNER-UNIT: FU-APP-WEB

package webscope

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/labeth/open-dso-core/webserver/wire"
	"github.com/labeth/open-sds1000cml/app/internal/engine"
	"github.com/labeth/open-sds1000cml/app/internal/panel"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
	"github.com/labeth/open-sds1000cml/app/internal/streamview"
)

// The route goldens: every route of the API answered for fixed scope states,
// recorded byte for byte (status, headers, body). A change of any served
// byte fails them; -update-route-goldens records the bytes served now.
var updateRouteGoldens = flag.Bool("update-route-goldens", false, "record the route goldens anew")

// routeReq is one request of a golden case.
type routeReq struct {
	method, path, body string
	header             map[string]string
	shown              string // the path as recorded, when the request's is filled in from earlier replies
}

// routeCase is a scope state and the requests recorded against it, in order,
// on one server.
type routeCase struct {
	name  string
	srv   func() *Server
	reqs  []routeReq
	after func(replies [][]byte, rq *routeReq) // fills a request in from the replies before it
}

// timeFields are the reply fields that carry the server's clock: their
// digits are recorded as zeros (same length, so binary layouts keep their
// header lengths).
var timeFields = regexp.MustCompile(`("(?:server_ms|serverMS|epoch)":)(\d+)`)

// TRLC-LINKS: REQ-SDS-161, REQ-SDS-162, REQ-SDS-163, REQ-SDS-164
func normalizeClock(b []byte) []byte {
	return timeFields.ReplaceAllFunc(b, func(m []byte) []byte {
		sub := timeFields.FindSubmatch(m)
		return append(append([]byte(nil), sub[1]...), bytes.Repeat([]byte("0"), len(sub[2]))...)
	})
}

// recordRoute serves one request and writes it as the golden text: the
// request, the status, every header and the body (text as it is, binary as
// a hex dump, large bodies as their length and hash).
// TRLC-LINKS: REQ-SDS-161
func recordRoute(t *testing.T, h http.Handler, rq routeReq, out *bytes.Buffer) *httptest.ResponseRecorder {
	t.Helper()
	var body io.Reader
	if rq.body != "" {
		body = strings.NewReader(rq.body)
	}
	req := httptest.NewRequestWithContext(t.Context(), rq.method, rq.path, body)
	for k, v := range rq.header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	shown := rq.path
	if rq.shown != "" {
		shown = rq.shown
	}
	fmt.Fprintf(out, "=== %s %s\n", rq.method, shown)
	if rq.body != "" {
		fmt.Fprintf(out, "request body: %s\n", rq.body)
	}
	for _, k := range slices.Sorted(func(yield func(string) bool) {
		for k := range rq.header {
			if !yield(k) {
				return
			}
		}
	}) {
		fmt.Fprintf(out, "request header %s: %s\n", k, rq.header[k])
	}
	fmt.Fprintf(out, "status %d\n", rec.Code)
	res := rec.Result()
	for _, k := range slices.Sorted(func(yield func(string) bool) {
		for k := range res.Header {
			if !yield(k) {
				return
			}
		}
	}) {
		fmt.Fprintf(out, "header %s: %s\n", k, strings.Join(res.Header[k], " | "))
	}
	for _, k := range slices.Sorted(func(yield func(string) bool) {
		for k := range res.Trailer {
			if !yield(k) {
				return
			}
		}
	}) {
		fmt.Fprintf(out, "trailer %s: %s\n", k, strings.Join(res.Trailer[k], " | "))
	}
	b := rec.Body.Bytes()
	if res.Header.Get("Content-Encoding") == "gzip" {
		zr, err := gzip.NewReader(bytes.NewReader(b))
		if err != nil {
			t.Fatalf("%s: gzip: %v", rq.path, err)
		}
		plain, err := io.ReadAll(zr)
		if err != nil {
			t.Fatalf("%s: gzip: %v", rq.path, err)
		}
		// The compressor's bytes are the library's; what it carries is ours.
		sum := sha256.Sum256(plain)
		fmt.Fprintf(out, "gzip body: %d bytes, sha256 %s\n\n", len(plain), hex.EncodeToString(sum[:]))
		return rec
	}
	if strings.HasPrefix(rq.path, "/debug/pprof") {
		// the runtime's own counters: their presence is the route's
		fmt.Fprintf(out, "body: runtime profile index\n\n")
		return rec
	}
	b = normalizeClock(b)
	sum := sha256.Sum256(b)
	fmt.Fprintf(out, "body: %d bytes, sha256 %s\n", len(b), hex.EncodeToString(sum[:]))
	switch ct := res.Header.Get("Content-Type"); {
	case len(b) > 6<<10:
	case strings.HasPrefix(ct, "application/json"), strings.HasPrefix(ct, "text/plain"):
		out.Write(b)
		if len(b) == 0 || b[len(b)-1] != '\n' {
			out.WriteByte('\n')
		}
	case len(b) > 0:
		out.WriteString(hex.Dump(b))
	}
	out.WriteByte('\n')
	return rec
}

// TRLC-LINKS: REQ-SDS-161, REQ-SDS-162, REQ-SDS-163, REQ-SDS-164
func TestRouteGoldens(t *testing.T) {
	for _, c := range routeCases() {
		t.Run(c.name, func(t *testing.T) {
			s := c.srv()
			h := s.Handler()
			var out bytes.Buffer
			var replies [][]byte
			for i := range c.reqs {
				rq := c.reqs[i]
				if c.after != nil {
					c.after(replies, &rq)
				}
				replies = append(replies, recordRoute(t, h, rq, &out).Body.Bytes())
			}
			path := filepath.Join(pkgDir, "testdata", "routes", c.name+".golden")
			if *updateRouteGoldens {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, out.Bytes(), 0o644); err != nil { //nolint:gosec // G306: a test golden, read by the test
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path) //nolint:gosec // G304: the golden named by the case
			if err != nil {
				t.Fatalf("%v (record with -update-route-goldens)", err)
			}
			if !bytes.Equal(out.Bytes(), want) {
				got, wnt := strings.Split(out.String(), "\n"), strings.Split(string(want), "\n")
				for i := range max(len(got), len(wnt)) {
					g, w := "", ""
					if i < len(got) {
						g = got[i]
					}
					if i < len(wnt) {
						w = wnt[i]
					}
					if g != w {
						t.Fatalf("%s differs at line %d:\n got: %s\nwant: %s", path, i+1, g, w)
					}
				}
			}
		})
	}
}

// goldenSine fills n codes of a sine of the given period and amplitude about mid.
// TRLC-LINKS: REQ-SDS-163
func goldenSine(n int, period, amp, mid float64) []uint8 {
	out := make([]uint8, n)
	for i := range out {
		out[i] = uint8(math.Round(mid + amp*math.Sin(2*math.Pi*float64(i)/period)))
	}
	return out
}

// goldenSquare fills n codes of a square wave between lo and hi.
// TRLC-LINKS: REQ-SDS-163
func goldenSquare(n, period int, lo, hi uint8) []uint8 {
	out := make([]uint8, n)
	for i := range out {
		out[i] = lo
		if i%period < period/2 {
			out[i] = hi
		}
	}
	return out
}

// goldenNative is a native-fast frame: interpolated, triggered mid-record.
// TRLC-LINKS: REQ-SDS-163
func goldenNative(seq uint64) *engine.Frame {
	n := 4096
	return &engine.Frame{
		Seq: seq, Valid: n, WinCols: 1000, EdgeX: 2048.25, Interp: true, SampleS: 2e-9, TdivS: 1e-7, DisplayedS: 1e-7,
		Trigd: true, Coherent: true, Ptp: 120, TriggerKind: "hardware-edge", Decimation: 1, FullRate: true,
		C1: goldenSine(n, 200, 60, 128), C2: goldenSquare(n, 250, 70, 190),
		StreamSeq: 3, WindowNs: 8192, GapNs: 400,
	}
}

// goldenDeep is a stopped decimated record: peak-detect pairs with precision
// words, held stretches, a web view window and a UART match end.
// TRLC-LINKS: REQ-SDS-163
func goldenDeep(seq uint64) *engine.Frame {
	n := 2048
	f := &engine.Frame{
		Seq: seq, Valid: n, WinCols: 1024, EdgeX: 1000.5, SampleS: 1e-6, CaptureSampleS: 2e-9, TdivS: 1e-4, DisplayedS: 1e-4,
		CaptureDepth: n, PeakDetect: true, Ordered: true, Norm: true, Trigd: true, Decimation: 500,
		BandwidthHz: 2e5, FilterGuard: 4, Filter: "fir", NoiseGainIdeal: 0.5, PassbandHz: 1.5e5, TriggerKind: "hardware-edge",
		C1: goldenSine(n, 300, 50, 120), C2: goldenSine(n, 97, 30, 140),
		HasMatchEnd: true, MatchEndS: 1.25e-5,
	}
	f.Q1, f.Q2 = make([]uint16, n), make([]uint16, n)
	for i := range n {
		f.Q1[i] = uint16(f.C1[i])<<8 | uint16(i%256)
		f.Q2[i] = uint16(f.C2[i])<<8 | uint16(255-i%256)
	}
	f.Held = make([]bool, n)
	for i := 100; i < 140; i++ {
		f.Held[i] = true
	}
	v := &engine.ViewWindow{Valid: 64, From: 900, To: 964, SampleS: 5e-7, PeakDetect: true,
		C1: goldenSine(64, 16, 40, 128), C2: goldenSquare(64, 8, 100, 160)}
	f.Views[engine.ViewWeb] = v
	return f
}

// goldenEnvelope is a roll-band envelope frame.
// TRLC-LINKS: REQ-SDS-163
func goldenEnvelope(seq uint64) *engine.Frame {
	cols := 800
	f := &engine.Frame{Seq: seq, IsEnv: true, EnvCols: cols, Valid: cols, WinCols: cols, EdgeX: -1, TdivS: 0.1, DisplayedS: 0.1, SampleS: 1e-3}
	f.C1, f.C2 = make([]uint8, cols), make([]uint8, cols)
	f.EnvMin, f.EnvMax = goldenSine(cols, 160, 30, 100), goldenSine(cols, 160, 30, 150)
	f.EnvMin2, f.EnvMax2 = goldenSquare(cols, 80, 40, 90), goldenSquare(cols, 80, 160, 210)
	return f
}

// goldenEngine is the fake scope with every optional surface the server
// asks for, each answering a fixed value.
type goldenEngine struct {
	*fakeScope
	sound   *soundFakeScope
	words   *wordsScope
	decoded *decodedWebScope
	notes   []string
}

// TRLC-LINKS: REQ-SDS-163
func (g *goldenEngine) LeaseDecodeView(d time.Duration) { g.notes = append(g.notes, "raw "+d.String()) }

// TRLC-LINKS: REQ-SDS-163
func (g *goldenEngine) LeaseSpectrumView(d time.Duration) {
	g.notes = append(g.notes, "spectrum "+d.String())
}

// TRLC-LINKS: REQ-SDS-163
func (g *goldenEngine) LeaseSampleView(d time.Duration) {
	g.notes = append(g.notes, "samples "+d.String())
}

// TRLC-LINKS: REQ-SDS-163
func (g *goldenEngine) LeaseSearchView(d time.Duration) {
	g.notes = append(g.notes, "search "+d.String())
}

// TRLC-LINKS: REQ-SDS-163
func (g *goldenEngine) LeaseViewWindow(_ engine.ViewSurface, a, b float64, cols int, d time.Duration) {
	g.notes = append(g.notes, fmt.Sprintf("view %g %g %d %v", a, b, cols, d))
}

// TRLC-LINKS: REQ-SDS-163
func (g *goldenEngine) WaitNextFrame(last uint64, _ time.Duration) uint64 { return last + 1 }

// TRLC-LINKS: REQ-SDS-164
func (g *goldenEngine) SincePublish() time.Duration { return 10 * time.Second }

// TRLC-LINKS: REQ-SDS-162
func (g *goldenEngine) SetRollStream(on bool) { g.notes = append(g.notes, fmt.Sprint("roll ", on)) }

// TRLC-LINKS: REQ-SDS-162
func (g *goldenEngine) SetPrecisionRate(r float64) float64 { return r / 2 }

// TRLC-LINKS: REQ-SDS-013
func (g *goldenEngine) EndWebDecodedCapture(context.Context) (bool, error) {
	g.notes = append(g.notes, "end web capture")
	return true, nil
}

// TRLC-LINKS: REQ-SDS-022
func (g *goldenEngine) SetPanelScan(p sramcapture.PanelConfig) error {
	if p.Half == 0 {
		return fmt.Errorf("half must be set")
	}
	return nil
}

// TRLC-LINKS: REQ-SDS-022
func (g *goldenEngine) PanelScan() (sramcapture.PanelFrame, error) {
	return sramcapture.PanelFrame{Words: [4]uint16{1, 2, 3, 4}, Knobs: [4]uint16{5, 6, 7, 8}, Frames: 9, Changes: 10, Pins: 11}, nil
}

// TRLC-LINKS: REQ-SDS-265
func (g *goldenEngine) LeaseSound(d time.Duration) { g.sound.LeaseSound(d) }

// TRLC-LINKS: REQ-SDS-265
func (g *goldenEngine) SoundInfo() engine.SoundInfo { return g.sound.SoundInfo() }

// TRLC-LINKS: REQ-SDS-265
func (g *goldenEngine) SoundRead(from uint64, limit int, q1, q2 []uint16) engine.SoundBlock {
	return g.sound.SoundRead(from, limit, q1, q2)
}

// TRLC-LINKS: REQ-SDS-265
func (g *goldenEngine) SoundWait(after uint64, timeout time.Duration) uint64 {
	return g.sound.SoundWait(after, timeout)
}

// TRLC-LINKS: REQ-SDS-275
func (g *goldenEngine) SoundTake() *engine.SoundTake {
	b := g.sound.SoundRead(1000, 6, nil, nil)
	for i := range b.Q1 {
		b.Q1[i], b.Q2[i] = uint16(32768+i*300), uint16(32768-i*200)
	}
	return &engine.SoundTake{SoundBlock: b, ID: 4, Why: "single", HasTrig: true, Trig: 1003, TrigCh: 1, TrigRising: true, TrigLevel: 150}
}

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (g *goldenEngine) ReadClientWords(seq uint64, limit int, wait time.Duration, dst []engine.ClientChunk) (engine.ClientWords, func()) {
	return g.words.ReadClientWords(seq, limit, wait, dst)
}

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (g *goldenEngine) PostClientFrames(b []byte) error { return g.words.PostClientFrames(b) }

// TRLC-LINKS: REQ-SDS-013
func (g *goldenEngine) BeginDecodedCapture(ctx context.Context, c sramcapture.Config) (sramcapture.RecordIdentity, error) {
	return g.decoded.BeginDecodedCapture(ctx, c)
}

// TRLC-LINKS: REQ-SDS-013
func (g *goldenEngine) ReadDecodedBatch(ctx context.Context, id sramcapture.RecordIdentity) ([]sramcapture.DecodedEvent, error) {
	return g.decoded.ReadDecodedBatch(ctx, id)
}

// TRLC-LINKS: REQ-SDS-013
func (g *goldenEngine) DecodedCaptureStatus(ctx context.Context, id sramcapture.RecordIdentity) (sramcapture.Metadata, error) {
	return g.decoded.DecodedCaptureStatus(ctx, id)
}

// TRLC-LINKS: REQ-SDS-013
func (g *goldenEngine) RecallDecodedRecord(ctx context.Context, id sramcapture.RecordIdentity, off, n uint32) ([]byte, error) {
	return g.decoded.RecallDecodedRecord(ctx, id, off, n)
}

// TRLC-LINKS: REQ-SDS-013
func (g *goldenEngine) EndDecodedCapture(ctx context.Context, id sramcapture.RecordIdentity) error {
	return g.decoded.EndDecodedCapture(ctx, id)
}

// TRLC-LINKS: REQ-SDS-141
func (g *goldenEngine) FPGAStack(_ context.Context, req engine.FPGAStackRequest) (engine.FPGAStackResult, error) {
	if req.Records > 100 {
		return engine.FPGAStackResult{}, fmt.Errorf("at most 100 records")
	}
	return engine.FPGAStackResult{Hits: 3, Crossings: 4, Records: req.Records, SampleS: 2e-9, Factor: 8}, nil
}

// newGoldenEngine is the full fake scope, running, with frame f.
// TRLC-LINKS: REQ-SDS-162
func newGoldenEngine(f *engine.Frame) *goldenEngine {
	fs := &fakeScope{
		frame: f,
		stats: engine.Stats{
			Frames: 77, Running: true, Norm: true, TdivS: 1e-7, DisplayedS: 1e-7, TrigCode: 30000, TrigSource: 0, TrigRising: true,
			TrigPosFrac: 0.5, OffC1: 10100, OffC2: 10300, MemDepth: 20480, MaskGen: 5, SerialMode: 1,
		},
		serialParams: engine.SerialParams{Proto: 1, Baud: 115200, Bits: 8, Addr: -1, Bytes: []int{0x55}, SPIClockHz: 1e6, SPIDelayNs: 3, Fine: true},
		maskRing: []engine.MaskFail{{
			C1: goldenSine(16, 8, 20, 128), C2: goldenSquare(16, 4, 90, 170), Valid: 16, Seq: 12, EdgeX: 7.5, SampleS: 1e-8,
			WinCols: 16, AtNs: 123456789, FailCol: 3, FailCode: 200, FailSample: 5,
		}},
		bodePts: []engine.BodePoint{{FreqHz: 1e3, GainDB: -0.1, PhaseDeg: -2}, {FreqHz: 1e4, GainDB: -3, PhaseDeg: -45}},
		acqLog:  []engine.AcqSample{{}},
		cmdLog:  []engine.CmdNote{{Name: "tdiv", Val: 1e-7}},
	}
	return &goldenEngine{
		fakeScope: fs,
		sound:     &soundFakeScope{fakeScope: fs, first: 1000, next: 1000 + 40},
		words:     &wordsScope{fakeScope: fs},
		decoded:   &decodedWebScope{fakeScope: fs, id: sramcapture.RecordIdentity{Epoch: 7, Record: 9}},
	}
}

// goldenPanel is the panel with every remote surface the server asks for.
type goldenPanel struct {
	remoteFixture
	sound   soundFakePanel
	lease   bool // the pages may hold SOUND (else the LCD does: paused)
	stream  bool
	cleared int
}

// TRLC-LINKS: REQ-SDS-220
func (p *goldenPanel) RemoteKey(name string, nomenu bool) panel.SetResult {
	return panel.SetResult{OK: true, Row: "key", Text: name, Changed: []string{"trig.level"}, Notice: fmt.Sprint("nomenu ", nomenu), SheetsSeq: 3}
}

// TRLC-LINKS: REQ-SDS-220
func (p *goldenPanel) RemoteKnob(name string, dir, steps int, _ bool) panel.SetResult {
	return panel.SetResult{OK: true, Row: "knob", Text: fmt.Sprintf("%s %d x%d", name, dir, steps)}
}

// TRLC-LINKS: REQ-SDS-226
func (p *goldenPanel) InjectAutoFor(ws string) bool { return ws == "bus" }

// TRLC-LINKS: REQ-SDS-228
func (p *goldenPanel) PageStream(on bool) panel.SetResult {
	return panel.SetResult{OK: true, Row: "bus.stream", Value: on}
}

// TRLC-LINKS: REQ-SDS-278
func (p *goldenPanel) PageLeave() panel.SetResult { return panel.SetResult{OK: true, Notice: "left"} }

// TRLC-LINKS: REQ-SDS-138
func (p *goldenPanel) RefsExport() [2]panel.RefExport {
	return [2]panel.RefExport{
		{Has: true, Show: true, Gen: 2, At: 1700000000000, Cols: 4, C1: []int{1, 2, 3, 4}, Vdiv: [2]float64{1, 0.5}, Probe: [2]float64{10, 1}, TdivS: 1e-3, PosFrac: 0.5,
			Setup: &panel.RefSetup{TdivS: 1e-3, PosFrac: 0.5, Vdiv: [2]float64{1, 0.5}, Rising: true}},
		{},
	}
}

// TRLC-LINKS: REQ-SDS-138
func (p *goldenPanel) RefAction(slot int, op string) panel.SetResult {
	if op != "store" {
		return panel.SetResult{Error: "no " + op}
	}
	return panel.SetResult{OK: true, Notice: fmt.Sprintf("REF %c stored", 'A'+slot)}
}

// TRLC-LINKS: REQ-SDS-018
func (p *goldenPanel) StreamStatus() (streamview.View, bool) {
	return streamview.View{State: streamview.Streaming, Label: "UART 115200 b/s C1", Units: 500, Lines: 30, TriggerLine: -1, Mode: "gap-free"}, p.stream
}

// TRLC-LINKS: REQ-SDS-018
func (p *goldenPanel) StreamWindow(rows int) (streamview.Page, bool) {
	pg := streamview.Page{Header: "t hex", Total: 30, Top: 30 - rows, TriggerLine: -1}
	for i := range rows {
		pg.Lines = append(pg.Lines, fmt.Sprintf("%d 55 AA", i))
	}
	return pg, p.stream
}

// TRLC-LINKS: REQ-SDS-018
func (p *goldenPanel) StreamLines(from, limit int) (streamview.LinesPage, bool) {
	pg := streamview.LinesPage{State: "STREAM", Base: 2, Next: from + min(limit, 2)}
	for i := range min(limit, 2) {
		pg.Lines = append(pg.Lines, streamview.LineData{Index: from + i, Sample: uint64(100 * i), Units: []uint32{0x55, 0xAA}, Errs: 2})
	}
	return pg, p.stream
}

// TRLC-LINKS: REQ-SDS-018
func (p *goldenPanel) StreamLinesBinary(from int, dst []byte) (int, bool) {
	return copy(dst, fmt.Sprintf("lines from %d", from)), p.stream
}

// TRLC-LINKS: REQ-SDS-164
func (p *goldenPanel) SuperresStatus() (active, review bool, bits float64, frames, rejected int, status string) {
	return true, false, 2.5, 40, 3, "stacking"
}

// TRLC-LINKS: REQ-SDS-277
func (p *goldenPanel) SoundHeld() string { return "the phone holds SOUND" }

// TRLC-LINKS: REQ-SDS-139
func (p *goldenPanel) RepeatCaption() (string, bool) { return "C1 repeats", true }

// TRLC-LINKS: REQ-SDS-014
func (p *goldenPanel) ClearMask() { p.cleared++ }

// TRLC-LINKS: REQ-SDS-274
func (p *goldenPanel) SoundSettings() panel.SoundSettings { return p.sound.SoundSettings() }

// TRLC-LINKS: REQ-SDS-274
func (p *goldenPanel) SetSoundSettings(s panel.SoundSettings) panel.SoundSettings {
	return p.sound.SetSoundSettings(s)
}

// TRLC-LINKS: REQ-SDS-274
func (p *goldenPanel) SoundAction(a string) error { return p.sound.SoundAction(a) }

// TRLC-LINKS: REQ-SDS-274
func (p *goldenPanel) SoundReport(api, marks bool) panel.SoundReport {
	return p.sound.SoundReport(api, marks)
}

// TRLC-LINKS: REQ-SDS-274
func (p *goldenPanel) SoundWant(api, marks bool) { p.sound.SoundWant(api, marks) }

// TRLC-LINKS: REQ-SDS-277
func (p *goldenPanel) SoundRemoteLease(_ string, lease func()) bool {
	if p.lease {
		lease()
	}
	return p.lease
}

// newGoldenPanel is the full panel fake with a fixed snapshot.
// TRLC-LINKS: REQ-SDS-162
func newGoldenPanel() *goldenPanel {
	p := &goldenPanel{lease: true, stream: true}
	p.status = panel.Status{
		Seq: 41, SheetsSeq: 9, Workspace: "SCOPE", WorkspaceID: "scope", View: "Y-T", Mode: "workspace",
		Again: "X-Y", AgainNext: "X-Y", Lamps: 0x21, LampsLit: []string{"CH1", "RUN"}, Screen: "on",
		Tool: []panel.ToolSeg{{Knob: "Adjust", Job: "Offset"}},
	}
	p.status.FKeys[0] = panel.KeyStatus{Label: "SCOPE", Sel: true}
	p.status.Display.ShowC1, p.status.Display.ShowMeas = true, true
	return p
}

// goldenSetups is a setup store that lists its slots.
type goldenSetups struct{ saved []int }

// TRLC-LINKS: REQ-SDS-219
func (g *goldenSetups) SaveSetup(n int) error {
	g.saved = append(g.saved, n)
	return nil
}

// TRLC-LINKS: REQ-SDS-219
func (g *goldenSetups) RecallSetup(n int) error {
	if n == 3 {
		return fmt.Errorf("slot 3 is empty")
	}
	return nil
}

// TRLC-LINKS: REQ-SDS-219
func (g *goldenSetups) SetupSlots() []wire.SetupSlot {
	return []wire.SetupSlot{{Slot: 1, Used: true, Summary: "SCOPE 1us/div"}, {Slot: 2}}
}

// goldenFull is a server on the full fakes: front end (CH2 AC, probes 10x
// and 1x), panel, screen, invert source and setups.
// TRLC-LINKS: REQ-SDS-162
func goldenFull(f *engine.Frame) (*Server, *goldenEngine, *goldenPanel) {
	g := newGoldenEngine(f)
	p := newGoldenPanel()
	fe := &fakeAnalog{idx: 8, set: true, probe: [2]float64{10, 1}, cpl: [2]int{0, 1}}
	s := newServer(g, fe, p, func() []byte { return []byte("\x89PNG fake screen") })
	s.SetInvertSource(func() [2]bool { return [2]bool{false, true} })
	s.SetSetupMemory(&goldenSetups{})
	return s, g, p
}

// rget and rpost are golden requests.
// TRLC-LINKS: REQ-SDS-161
func rget(path string) routeReq { return routeReq{method: http.MethodGet, path: path} }

// TRLC-LINKS: REQ-SDS-162
func rpost(path, body string) routeReq {
	return routeReq{method: http.MethodPost, path: path, body: body}
}

// routeCases are the recorded scope states and requests.
// TRLC-LINKS: REQ-SDS-161, REQ-SDS-162, REQ-SDS-163, REQ-SDS-164
func routeCases() []routeCase { //nolint:funlen // one table of every route
	full := func(f func(uint64) *engine.Frame) func() *Server {
		return func() *Server { s, _, _ := goldenFull(f(31)); return s }
	}
	bare := func() *Server { return newServer(&fakeScope{frame: goldenNative(31)}, nil, nil, nil) }
	return []routeCase{
		{name: "pages", srv: bare, reqs: []routeReq{
			rget("/"), rget("/remote"), rget("/r"), rget("/remote/"), rget("/app_core.js"), rget("/base.css"),
			rget("/remote.webmanifest"), rget("/remote-icon-180.png"), rget("/nope.js"), rget("/a/b.js"), rget("/ui.html"),
			{method: http.MethodGet, path: "/app_core.js", header: map[string]string{"Accept-Encoding": "gzip"}},
			{method: http.MethodGet, path: "/remote-awake.mp4", header: map[string]string{"Range": "bytes=0-15"}},
			{method: http.MethodGet, path: "/", header: map[string]string{"If-None-Match": "\"x\""}},
			rget("/debug/pprof/"),
		}},
		{name: "claim", srv: full(goldenNative), reqs: []routeReq{
			rget("/api/frame.bin?epoch=5&cols=64"), rget("/api/claim"), {method: http.MethodPost, path: "/api/claim"},
			rget("/api/frame.bin?epoch=5&cols=64"), rget("/api/frame.bin?epoch=CLAIMED&cols=64"), rget("/api/frame.bin?epoch=0&cols=64"),
		}, after: func(replies [][]byte, rq *routeReq) {
			if !strings.Contains(rq.path, "CLAIMED") {
				return
			}
			var c struct{ Epoch uint64 }
			if err := json.Unmarshal(replies[2], &c); err != nil {
				panic(err)
			}
			rq.shown, rq.path = rq.path, strings.Replace(rq.path, "CLAIMED", fmt.Sprint(c.Epoch), 1)
		}},
		{name: "status_full", srv: full(goldenDeep), reqs: []routeReq{rget("/api/status")}},
		{name: "status_bare", srv: bare, reqs: []routeReq{rget("/api/status"), rpost("/api/status", "{}")}},
		{name: "frame_native", srv: full(goldenNative), reqs: []routeReq{
			rget("/api/frame.bin"), rget("/api/frame.bin?cols=10"), rget("/api/frame.bin?cols=99999&since=3&waitms=5"),
			rget("/api/frame.bin?since=31"), rget("/api/frame.bin?full=1&cols=640"),
		}},
		{name: "frame_native_bare", srv: bare, reqs: []routeReq{rget("/api/frame.bin?cols=128"), rget("/api/frame.bin?raw=1")}},
		{name: "frame_deep", srv: full(goldenDeep), reqs: []routeReq{
			rget("/api/frame.bin?full=1"), rget("/api/frame.bin?cols=300"), rget("/api/frame.bin?raw=1"), rget("/api/frame.bin?raw=1&since=31"),
		}},
		{name: "frame_envelope", srv: full(goldenEnvelope), reqs: []routeReq{rget("/api/frame.bin"), rget("/api/frame.bin?cols=100&raw=1")}},
		{name: "frame_empty", srv: func() *Server { return newServer(&fakeScope{}, nil, nil, nil) }, reqs: []routeReq{
			rget("/api/frame.bin"), rget("/api/frame.bin?raw=1"),
		}},
		{name: "frame_leases", srv: full(goldenNative), reqs: []routeReq{
			rget("/api/frame.bin?lease=raw&cols=64"), rget("/api/frame.bin?lease=raw&end=1"),
			rget("/api/frame.bin?lease=spectrum&cols=64"), rget("/api/frame.bin?lease=spectrum&end=1"),
			rget("/api/frame.bin?lease=samples&end=1"), rget("/api/frame.bin?lease=search&end=1"),
			rget("/api/frame.bin?view=0.1,0.2,1200&cols=64"), rget("/api/frame.bin?view=off&cols=64"),
		}},
		{name: "frame_leases_bare", srv: bare, reqs: []routeReq{rget("/api/frame.bin?lease=raw&end=1&cols=64")}},
		{name: "set", srv: full(goldenNative), reqs: []routeReq{
			rget("/api/set"), rpost("/api/set", "{"), rpost("/api/set", `{"control":"tdiv","value":1e-6}`),
			rpost("/api/set", `{"control":"tdiv","value":3e-6}`), rpost("/api/set", `{"control":"run","value":0}`),
			rpost("/api/set", `{"control":"runtoggle"}`), rpost("/api/set", `{"control":"single"}`),
			rpost("/api/set", `{"control":"norm","value":1}`), rpost("/api/set", `{"control":"triglevelcode","value":70000}`),
			rpost("/api/set", `{"control":"triglevelcode","value":30000}`), rpost("/api/set", `{"control":"trigslope","value":1}`),
			rpost("/api/set", `{"control":"ets","value":1}`), rpost("/api/set", `{"control":"trigpos","value":0.3}`),
			rpost("/api/set", `{"control":"memdepth","value":4096}`), rpost("/api/set", `{"control":"frameperiod","value":40}`),
			rpost("/api/set", `{"control":"decodeview","value":1}`), rpost("/api/set", `{"control":"rollstream","value":1}`),
			rpost("/api/set", `{"control":"holdoff","value":0.001}`), rpost("/api/set", `{"control":"stream","value":1}`),
			rpost("/api/set", `{"control":"serialmode","value":2}`), rpost("/api/set", `{"control":"zonemode","value":1}`),
			rpost("/api/set", `{"control":"maskmode","value":1}`), rpost("/api/set", `{"control":"maskclear"}`),
			rpost("/api/set", `{"control":"bodemode","value":1,"lo":0,"hi":1}`), rpost("/api/set", `{"control":"bodeclear"}`),
			rpost("/api/set", `{"control":"trigtype","value":2}`),
			rpost("/api/set", `{"control":"pulseparams","lvl":0.5,"min":10,"max":20,"cond":3}`),
			rpost("/api/set", `{"control":"slopeparams","lo":0.1,"hi":0.9,"min":1,"max":5,"cond":1}`),
			rpost("/api/set", `{"control":"videoparams","std":1,"line":20,"neg":true}`),
			rpost("/api/set", `{"control":"acqmode","value":2}`), rpost("/api/set", `{"control":"precisionrate","value":1e6}`),
			rpost("/api/set", `{"control":"avgcount","value":16}`), rpost("/api/set", `{"control":"eres","value":4}`),
			rpost("/api/set", `{"control":"eresbits","value":1.5}`), rpost("/api/set", `{"control":"trigsource","value":1}`),
			rpost("/api/set", `{"control":"vdiv1","value":0.5}`), rpost("/api/set", `{"control":"vdiv2","value":0.3}`),
			rpost("/api/set", `{"control":"offset1","value":0.25}`), rpost("/api/set", `{"control":"offset2","value":99}`),
			rpost("/api/set", `{"control":"probe2","value":10}`), rpost("/api/set", `{"control":"probe1","value":5}`),
			rpost("/api/set", `{"control":"coupling2","value":2}`), rpost("/api/set", `{"control":"coupling1","value":7}`),
			rpost("/api/set", `{"control":"bwl1","value":1}`), rpost("/api/set", `{"control":"nope"}`),
			rpost("/api/set", `{"control":"tdiv","value":1e999}`), rpost("/api/set", `{"control":"tdiv","value":1} {"x":2}`),
			rget("/api/status"),
		}},
		{name: "set_bare", srv: bare, reqs: []routeReq{
			rpost("/api/set", `{"control":"vdiv1","value":0.5}`), rpost("/api/set", `{"control":"offset2","value":0.5}`),
			rpost("/api/set", `{"control":"probe1","value":10}`), rpost("/api/set", `{"control":"coupling1","value":1}`),
			rpost("/api/set", `{"control":"bwl2","value":1}`), rpost("/api/set", `{"control":"rollstream","value":1}`),
			rpost("/api/set", `{"control":"precisionrate","value":1e6}`), rpost("/api/set", `{"control":"ets","value":1}`),
		}},
		{name: "panel", srv: full(goldenNative), reqs: []routeReq{
			rpost("/api/panel", "x"), rpost("/api/panel", `{"button":"F1"}`), rpost("/api/panel", `{"knob":"adjust","dir":-1}`),
			rpost("/api/panel", `{"button":"F2","remote":true,"nomenu":true}`), rpost("/api/panel", `{"knob":"adjust","dir":1,"steps":3,"remote":true}`),
			rpost("/api/panel", `{"button":"auto","for":"bus"}`), rpost("/api/panel", `{"button":"AUTO","for":"scope"}`),
			rpost("/api/panel", `{"type":{"text":"55 AA"}}`), rpost("/api/panel", `{"set":{"row":"trig.level","text":"50V"}}`),
			rpost("/api/panel", `{"workspace":"measure"}`), rpost("/api/panel", `{"again":true}`), rpost("/api/panel", `{"workspace":"nope"}`),
			rpost("/api/panel", `{}`),
		}},
		{name: "panel_bare", srv: bare, reqs: []routeReq{
			rpost("/api/panel", `{"button":"F1"}`), rget("/api/panel/wait"), rget("/api/sheets"), rpost("/api/sheets/set", `{"row":"a","value":1}`),
			rpost("/api/sheets/action", `{"row":"a"}`), rpost("/api/stream/page", `{"on":true}`), rget("/api/refs"),
		}},
		{name: "panel_keys_only", srv: func() *Server {
			return newServer(&fakeScope{}, nil, &autoPanel{}, nil)
		}, reqs: []routeReq{
			rpost("/api/panel", `{"button":"F1","remote":true}`), rpost("/api/panel", `{"type":{"text":"1"}}`),
			rpost("/api/panel", `{"workspace":"bus"}`), rpost("/api/panel", `{"button":"auto","for":"bus"}`),
		}},
		{name: "remote", srv: full(goldenNative), reqs: []routeReq{
			rget("/api/panel/wait?seq=40&timeout_ms=20000"), rget("/api/panel/wait?timeout_ms=-3"), rget("/api/sheets"),
			{method: http.MethodPost, path: "/api/sheets"}, rpost("/api/sheets/set", `{"row":"trig.level","value":1.25}`),
			rpost("/api/sheets/set", `{"row":"trig.level","text":"50V"}`), rpost("/api/sheets/set", `{"value":1}`),
			rpost("/api/sheets/action", `{"row":"file.setup3","action":"save"}`), rpost("/api/sheets/action", `{"row":""}`),
			rpost("/api/stream/page", `{"on":true}`), rpost("/api/stream/page", `{"leave":true}`), rpost("/api/stream/page", `[`),
			rget("/api/stream/page"),
		}},
		{name: "remote_fixture", srv: func() *Server {
			return newServer(&fakeScope{}, nil, &panelFixture{status: panel.Status{Seq: 2}}, nil)
		}, reqs: []routeReq{rpost("/api/stream/page", `{"on":true}`), rpost("/api/stream/page", `{"leave":true}`), rpost("/api/panel", `{"again":true}`)}},
		{name: "refs", srv: full(goldenNative), reqs: []routeReq{
			rget("/api/refs"), rpost("/api/refs", `{"slot":"B","op":"store"}`), rpost("/api/refs", `{"slot":"A","op":"hide"}`),
			rpost("/api/refs", `{"slot":"C","op":"store"}`), {method: http.MethodPut, path: "/api/refs"},
		}},
		{name: "stream", srv: full(goldenNative), reqs: []routeReq{
			rget("/api/stream/lines?from=5&max=99999"), rget("/api/stream/lines?from=x&max=1"), rget("/api/stream/lines.bin?from=12"),
			rget("/api/stream/window?rows=3"), rget("/api/stream/window?rows=999"), rpost("/api/stream/lines", "{}"),
		}},
		{name: "stream_off", srv: func() *Server {
			s, _, p := goldenFull(goldenNative(31))
			p.stream = false
			return s
		}, reqs: []routeReq{rget("/api/stream/lines"), rget("/api/stream/lines.bin"), rget("/api/stream/window"), rget("/api/status")}},
		{name: "stream_bare", srv: bare, reqs: []routeReq{
			rget("/api/stream/lines"), rget("/api/stream/lines.bin"), rget("/api/stream/window"), rget("/api/stream/words.bin"),
			rpost("/api/stream/frames.bin", "x"),
		}},
		{name: "words", srv: full(goldenNative), reqs: []routeReq{
			rget("/api/stream/words.bin?from=7&max=99999999&wait=99999"), rget("/api/stream/words.bin"),
			rpost("/api/stream/frames.bin", "nope"), rpost("/api/stream/frames.bin", "SDF1"+strings.Repeat("\x00", 16)),
		}},
		{name: "record_refused", srv: full(goldenNative), reqs: []routeReq{
			rget("/api/record.bin"), rget("/api/record.bin?sample_s=1e-10"), rget("/api/record.bin?sample_s=2"), rget("/api/record.bin?sample_s=1e-6&wait_ms=1"),
		}},
		{name: "record", srv: func() *Server {
			sc := &recordScope{fakeScope: &fakeScope{stats: engine.Stats{Running: true}}}
			sc.init()
			return newServer(sc, &fakeAnalog{idx: 8, probe: [2]float64{10, 10}}, nil, nil)
		}, reqs: []routeReq{rget("/api/record.bin?sample_s=1e-8&wait_ms=2000")}},
		{name: "record_stopped", srv: func() *Server {
			sc := &recordScope{fakeScope: &fakeScope{}, never: true}
			sc.init()
			return newServer(sc, nil, nil, nil)
		}, reqs: []routeReq{rget("/api/record.bin?sample_s=1e-8&wait_ms=30")}},
		{name: "triggers", srv: full(goldenNative), reqs: []routeReq{
			rget("/api/zones"), rpost("/api/zones", `[{"dt_lo_s":1e-6,"dt_hi_s":2e-6,"code_lo":-5,"code_hi":300,"avoid":true,"ch":3},{"dt_lo_s":0,"dt_hi_s":1,"code_lo":1,"code_hi":2},{},{},{}]`),
			rpost("/api/zones", "{"), rget("/api/serial"),
			rpost("/api/serial", `{"proto":99,"chA":2,"chB":3,"rw":5,"addr":200,"baud":-1,"bits":99,"bytes":[1,99999,-4],"spiClockHz":5}`),
			rpost("/api/serial", `{"proto":30,"bits":99,"bytes":[70000]}`), rpost("/api/serial", `{"proto":8,"bytes":[999999]}`),
			rpost("/api/serial", "x"), {method: http.MethodPut, path: "/api/serial"}, rget("/api/serial/protocols"),
			rget("/api/serial"),
		}},
		{name: "serial_bare", srv: func() *Server {
			return newServer(&serialLessScope{fakeScope: &fakeScope{}}, nil, nil, nil)
		}, reqs: []routeReq{rget("/api/serial"), rpost("/api/serial", `{"proto":1,"bytes":[300]}`)}},
		{name: "masks", srv: full(goldenNative), reqs: []routeReq{
			rget("/api/mask"), rpost("/api/mask", `{"lo":[1,2],"hi":[3],"win":2}`), rpost("/api/mask", `{"lo":[-1,300],"hi":[10,20],"win":2,"ch":3,"tdiv_s":1e-6,"sample_s":1e-8}`),
			rpost("/api/mask", `{"lo_b64":"AQI=","hi_b64":"AwQ=","win":2,"if_gen":6}`), rpost("/api/mask", `{"lo_b64":"AQI=","hi_b64":"AwQ=","win":2,"if_gen":1}`),
			rpost("/api/mask", `{"lo_b64":"AQI=","hi_b64":"AwQ=","win":3}`), rpost("/api/mask", `{"lo":[]}`), rpost("/api/mask", "["),
			rget("/api/maskfail"), rget("/api/maskfail?i=0"), rget("/api/maskfail?i=1"), rget("/api/maskfail?i=x"),
			rget("/api/bode"), rpost("/api/bode", "{}"),
		}},
		{name: "masks_bare", srv: bare, reqs: []routeReq{rpost("/api/mask", `{"lo":[]}`), rget("/api/maskfail"), rget("/api/bode")}},
		{name: "sound", srv: full(goldenNative), reqs: []routeReq{
			rget("/api/sound.bin?from=1010&max=8&marks=1"), rget("/api/sound.bin?max=99999999&min=4&wait=5"), rget("/api/sound.bin?from=2000&wait=1"),
			rget("/api/sound/take.bin"), rget("/api/sound"), rget("/api/sound?api=1&marks=1&lease=0"),
			rpost("/api/sound", `{"settings":{"a4":442,"ch":1},"action":"adsr_arm"}`), rpost("/api/sound", `{"action":"take_scope"}`),
			rpost("/api/sound", `{"settings":7}`), rpost("/api/sound", `{"action":"nope"}`), rpost("/api/sound", "{"),
		}},
		{name: "sound_paused", srv: func() *Server {
			s, _, p := goldenFull(goldenNative(31))
			p.lease = false
			return s
		}, reqs: []routeReq{rget("/api/sound.bin?who=phone"), rget("/api/sound/take.bin?who=phone"), rget("/api/sound?who=phone")}},
		{name: "sound_bare", srv: bare, reqs: []routeReq{rget("/api/sound.bin"), rget("/api/sound/take.bin"), rget("/api/sound")}},
		{name: "sound_no_panel", srv: func() *Server {
			g := newGoldenEngine(goldenNative(31))
			return newServer(g, nil, nil, nil)
		}, reqs: []routeReq{rget("/api/sound.bin?from=1000&max=2"), rget("/api/sound/take.bin"), rget("/api/sound")}},
		{name: "screen_setup_tune", srv: full(goldenNative), reqs: []routeReq{
			rget("/api/screen.png"), rget("/api/setup"), rpost("/api/setup", `{"op":"save","slot":4}`), rpost("/api/setup", `{"op":"recall","slot":3}`),
			rpost("/api/setup", `{"op":"load","slot":4}`), rpost("/api/setup", `{"op":"save","slot":21}`), rpost("/api/setup", "{"),
			{method: http.MethodDelete, path: "/api/setup"}, rget("/api/debug/tune"), rpost("/api/debug/tune", `{"max_retry":3,"gc_ctl":true}`),
			rpost("/api/debug/tune", "x"), rget("/api/panel/scan"), rpost("/api/panel/scan", `{"enable":true,"half":20}`),
			rpost("/api/panel/scan", `{"enable":true}`), rpost("/api/panel/scan", "x"),
		}},
		{name: "screen_setup_bare", srv: bare, reqs: []routeReq{
			rget("/api/screen.png"), rget("/api/setup"), rpost("/api/setup", `{"op":"save","slot":4}`), rpost("/api/setup", `{"op":"save","slot":0}`),
			rget("/api/panel/scan"),
		}},
		{name: "extensions", srv: full(goldenNative), reqs: []routeReq{
			rpost("/api/decoded/start", `{}`), rpost("/api/decoded/start", `{"nope":1}`), rpost("/api/decoded/start", `{} {}`),
			rget("/api/decoded/status?epoch=7&record=9"), rget("/api/decoded/status?epoch=7"), rget("/api/decoded/status?record=1"),
			rget("/api/decoded/record.bin?epoch=7&record=9&offset=0&words=4"), rget("/api/decoded/record.bin?epoch=7&record=8&offset=0&words=4"),
			rget("/api/decoded/record.bin?epoch=7&record=9&offset=1"), rget("/api/decoded/record.bin?epoch=7&record=9&words=1"),
			rget("/api/decoded/events.bin?epoch=7&record=9"), rget("/api/decoded/events.bin?epoch=7&record=1"),
			rpost("/api/decoded/end?epoch=7&record=9", ""), rget("/api/decoded/end?epoch=7&record=9"),
			rpost("/api/superres/fpga", `{"records":20}`), rpost("/api/superres/fpga", `{"records":200}`),
			rpost("/api/superres/fpga", `{"records":2,"bad":1}`), rpost("/api/superres/fpga", `{"records":2} {}`),
			rpost("/api/superres/fpga", `{"records":2,"stack":{"channel":1,"level":128},"match":{"points":16,"tolerance":0.2}}`),
			rget("/api/superres/fpga"),
		}},
		{name: "extensions_bare", srv: bare, reqs: []routeReq{
			rpost("/api/decoded/start", `{}`), rpost("/api/superres/fpga", `{"records":20}`),
		}},
	}
}

// serialLessScope is a scope without the serial trigger's read-back.
type serialLessScope struct{ *fakeScope }

// SerialConfig is shadowed so the type has no such method set: a scope
// whose trigger cannot be read back.
// TRLC-LINKS: REQ-SDS-162
func (serialLessScope) SerialConfig(int) {}
