package web

import (
	"math"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"open-sds/app/internal/analog"
	"open-sds/app/internal/engine"
	"open-sds/app/internal/testenv"
)

// ---------------------------------------------------------------------------
// A device-free lane for the operator fuzzer (e2e/fuzz.mjs): the fuzzer's
// primary target is the LIVE scope, but every PR should still get a bounded,
// deterministic random-operator campaign. That needs a synthetic engine whose
// CONTROLS RESPOND — the static fakeScope serves frames but ignores RUN/STOP,
// tdiv, trigger level, … so half the fuzz palette would be inert against it.
//
// fuzzScope is that stub: a mutex-guarded, STATEFUL Scope whose Snapshot()
// reflects every staging setter the web /api/set paths call, and whose frame
// stream obeys the acquisition state the way the real engine does:
//   - RUN/STOP gates seq advance (STOP freezes the display, RUN resumes),
//   - SINGLE arms one more capture and then self-stops (running+single drop),
//   - tdiv changes flow into the next frame's TdivS/SampleS,
//   - NORM keeps publishing (the synthetic signal always qualifies) with the
//     frame flagged Trigd, exactly like a live scope on a healthy signal.
// ---------------------------------------------------------------------------

const fuzzN = 2048 // record length: native display window, same as chaos/acceptance

type fuzzScope struct {
	mu       sync.Mutex
	seq      uint64
	running  bool
	norm     bool
	single   bool
	tdiv     float64
	trigCode uint16
	trigRise bool
	trigSrc  int
	trigPos  float64
	holdoff  float64
	ets      bool
	trigType int
	acqMode  int
	avgCount int
	eresLen  int
	memDepth int
	zoneMode int
	zones    []engine.Zone
	maskMode int
	maskSet  bool
	serMode  int
	serSet   bool
	bodeMode int
	cmds     []engine.CmdNote
}

func newFuzzScope() *fuzzScope {
	return &fuzzScope{
		running:  true,
		tdiv:     500e-6,
		trigCode: 31434, // ≈ 0 V: the level marker lands mid-screen (draggable)
		trigRise: true,
		trigPos:  0.5,
		avgCount: 16,
		eresLen:  1,
	}
}

// frameLocked renders the synthetic two-tone frame for the CURRENT seq/tdiv.
// Pure function of (seq, tdiv) so a given SEED replays identically.
func (z *fuzzScope) frameLocked() *engine.Frame {
	c1 := make([]uint8, fuzzN)
	c2 := make([]uint8, fuzzN)
	ph := float64(z.seq) * 0.05
	for i := 0; i < fuzzN; i++ {
		c1[i] = uint8(128 + 60*math.Sin(2*math.Pi*8*float64(i)/fuzzN+ph))
		c2[i] = uint8(128 + 40*math.Sin(2*math.Pi*3*float64(i)/fuzzN))
	}
	return &engine.Frame{
		C1: c1, C2: c2, Seq: z.seq, Valid: fuzzN, WinCols: fuzzN, EdgeX: fuzzN / 2,
		TdivS: z.tdiv, DisplayedS: z.tdiv, SampleS: z.tdiv * 10 / fuzzN,
		Trigd: true, Coherent: true, Norm: z.norm, Ptp: 120,
	}
}

// WithFrame advances the stream only while RUNNING (a stopped scope's display
// holds); an armed SINGLE takes exactly one more capture and self-stops, which
// the UI's fast post-single status poll then observes (run-after-single fix).
func (z *fuzzScope) WithFrame(fn func(*engine.Frame)) {
	z.mu.Lock()
	if z.running {
		z.seq++
		if z.single {
			z.single, z.running = false, false
		}
	}
	f := z.frameLocked()
	z.mu.Unlock()
	fn(f)
}

func (z *fuzzScope) Snapshot() engine.Stats {
	z.mu.Lock()
	defer z.mu.Unlock()
	return engine.Stats{
		Frames: z.seq, Coherent: z.seq, Published: z.seq, Seq: z.seq,
		FPS: 20, Running: z.running, Norm: z.norm, Single: z.single,
		TrigPosFrac: z.trigPos, TdivS: z.tdiv, DisplayedS: z.tdiv,
		TrigCode: z.trigCode, TrigRising: z.trigRise, TrigSource: z.trigSrc,
		HoldoffS: z.holdoff, ETS: z.ets, TrigType: z.trigType,
		AcqMode: z.acqMode, AvgCount: z.avgCount, EresLen: z.eresLen,
		BandKind: bandKindName(z.tdiv), HaltMode: "capture-halt",
		LastPtp: 120, MmapDrain: true, WinCols: fuzzN, MemDepth: z.memDepth,
		ZoneMode: z.zoneMode, ZoneCount: len(z.zones),
		MaskMode: z.maskMode, MaskSet: z.maskSet,
		SerialMode: z.serMode, SerialSet: z.serSet,
		BodeMode: z.bodeMode,
	}
}

// bandKindName mirrors the engine's tdiv→band classification strings so the
// status line renders a plausible band label at any ladder position.
func bandKindName(tdivS float64) string {
	switch {
	case tdivS >= 100e-3:
		return "roll"
	case tdivS >= 5e-3:
		return "envelope"
	case tdivS <= 100e-6:
		return "native-fast"
	default:
		return "decimated"
	}
}

func (z *fuzzScope) QuietRLock()   {}
func (z *fuzzScope) QuietRUnlock() {}

func (z *fuzzScope) SetRunning(on bool) {
	z.mu.Lock()
	z.running = on
	if !on {
		z.single = false
	}
	z.mu.Unlock()
}
func (z *fuzzScope) SetNorm(on bool) { z.mu.Lock(); z.norm = on; z.mu.Unlock() }
func (z *fuzzScope) SetSingle()      { z.mu.Lock(); z.single, z.running = true, true; z.mu.Unlock() }
func (z *fuzzScope) SetTdiv(s float64) (engine.Band, bool) {
	b, ok := engine.PlanTdiv(s)
	if ok {
		z.mu.Lock()
		z.tdiv = b.TdivS
		z.mu.Unlock()
	}
	return b, ok
}
func (z *fuzzScope) SetTrigLevelCode(c uint16) uint16 {
	if c < engine.TrigCodeMin {
		c = engine.TrigCodeMin
	}
	if c > engine.TrigCodeMax {
		c = engine.TrigCodeMax
	}
	z.mu.Lock()
	z.trigCode = c
	z.mu.Unlock()
	return c
}
func (z *fuzzScope) SetTrigSlope(r bool)                                 { z.mu.Lock(); z.trigRise = r; z.mu.Unlock() }
func (z *fuzzScope) SetTrigSource(ch int)                                { z.mu.Lock(); z.trigSrc = ch & 1; z.mu.Unlock() }
func (z *fuzzScope) SetOffsetDAC(ch int, code uint16)                    {}
func (z *fuzzScope) SetETS(on bool)                                      { z.mu.Lock(); z.ets = on; z.mu.Unlock() }
func (z *fuzzScope) SetTrigType(t int)                                   { z.mu.Lock(); z.trigType = t; z.mu.Unlock() }
func (z *fuzzScope) SetPulseParams(lvl, wMin, wMax float64, cond int)    {}
func (z *fuzzScope) SetSlopeParams(lo, hi, tMin, tMax float64, cond int) {}
func (z *fuzzScope) SetVideoParams(std, line int, neg bool)              {}
func (z *fuzzScope) SetAcqMode(m int)                                    { z.mu.Lock(); z.acqMode = m; z.mu.Unlock() }
func (z *fuzzScope) SetAvgCount(n int)                                   { z.mu.Lock(); z.avgCount = n; z.mu.Unlock() }
func (z *fuzzScope) SetEresLen(l int)                                    { z.mu.Lock(); z.eresLen = l; z.mu.Unlock() }
func (z *fuzzScope) SetTrigPosFrac(frac float64)                         { z.mu.Lock(); z.trigPos = frac; z.mu.Unlock() }
func (z *fuzzScope) SetMemDepth(n int) int                               { z.mu.Lock(); z.memDepth = n; z.mu.Unlock(); return n }
func (z *fuzzScope) SetFramePeriod(ms int) int                           { return ms }
func (z *fuzzScope) SetStreamMode(on bool) bool                          { return on }
func (z *fuzzScope) SetHoldoff(sec float64) float64 {
	z.mu.Lock()
	z.holdoff = sec
	z.mu.Unlock()
	return sec
}
func (z *fuzzScope) SetZones(zs []engine.Zone)    { z.mu.Lock(); z.zones = zs; z.mu.Unlock() }
func (z *fuzzScope) SetZoneMode(m int)            { z.mu.Lock(); z.zoneMode = m; z.mu.Unlock() }
func (z *fuzzScope) SetMask(m *engine.Mask)       { z.mu.Lock(); z.maskSet = m != nil; z.mu.Unlock() }
func (z *fuzzScope) SetMaskMode(m int)            { z.mu.Lock(); z.maskMode = m; z.mu.Unlock() }
func (z *fuzzScope) ClearMaskFails()              {}
func (z *fuzzScope) MaskFails() []engine.MaskFail { return nil }
func (z *fuzzScope) SetSerialParams(p engine.SerialParams) {
	z.mu.Lock()
	z.serSet = p.Proto > 0
	z.mu.Unlock()
}
func (z *fuzzScope) SetSerialMode(m int) { z.mu.Lock(); z.serMode = m; z.mu.Unlock() }
func (z *fuzzScope) SetBodeMode(on bool, ref, dut int) {
	z.mu.Lock()
	if on {
		z.bodeMode = 1
	} else {
		z.bodeMode = 0
	}
	z.mu.Unlock()
}
func (z *fuzzScope) ClearBode()                     {}
func (z *fuzzScope) BodePoints() []engine.BodePoint { return nil }

func (z *fuzzScope) AcqLog(n int) ([]engine.AcqSample, float64) { return nil, 0 }
func (z *fuzzScope) CmdLog(n int) []engine.CmdNote {
	z.mu.Lock()
	defer z.mu.Unlock()
	if len(z.cmds) > n {
		return append([]engine.CmdNote(nil), z.cmds[len(z.cmds)-n:]...)
	}
	return append([]engine.CmdNote(nil), z.cmds...)
}
func (z *fuzzScope) NoteCmd(name string, val float64) {
	z.mu.Lock()
	z.cmds = append(z.cmds, engine.CmdNote{Name: name, Val: val})
	if len(z.cmds) > 16 {
		z.cmds = z.cmds[len(z.cmds)-16:]
	}
	z.mu.Unlock()
}
func (z *fuzzScope) Tune(t engine.TuneVals) engine.TuneVals { return t }
func (z *fuzzScope) TuneSnapshot() engine.TuneVals          { return engine.TuneVals{} }

// fuzzAnalog is a per-channel stateful Analog stub (fakeAnalog deliberately
// shares one detent across channels, which would make vdiv1/vdiv2 fight):
// V/div, probe and coupling all read back what was set, so the vertical
// /api/set paths respond and /api/status carries the ladder + live values.
type fuzzAnalog struct {
	mu    sync.Mutex
	idx   [2]int
	probe [2]float64
	cpl   [2]int
}

func newFuzzAnalog() *fuzzAnalog {
	return &fuzzAnalog{idx: [2]int{analog.BootDetent, analog.BootDetent}} // 1 V/div both
}

func (a *fuzzAnalog) SetVdiv(ch, idx int) error {
	a.mu.Lock()
	a.idx[ch&1] = idx
	a.mu.Unlock()
	return nil
}
func (a *fuzzAnalog) Snapshot() ([2]int, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.idx, true
}
func (a *fuzzAnalog) SetOffset(ch int, volts float64) uint16 { return analog.OffsetCode(ch, volts) }
func (a *fuzzAnalog) OffsetVolts(ch int, code uint16) float64 {
	return analog.OffsetVolts(ch, code)
}
func (a *fuzzAnalog) CalSource() string                    { return "synthetic" }
func (a *fuzzAnalog) DCVolts(ch int, mean float64) float64 { return 0 }
func (a *fuzzAnalog) SetProbe(ch int, x float64)           { a.mu.Lock(); a.probe[ch&1] = x; a.mu.Unlock() }
func (a *fuzzAnalog) ProbeFactor(ch int) float64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	if p := a.probe[ch&1]; p >= 1 {
		return p
	}
	return 1
}
func (a *fuzzAnalog) SetCoupling(ch, mode int) error {
	a.mu.Lock()
	a.cpl[ch&1] = mode
	a.mu.Unlock()
	return nil
}
func (a *fuzzAnalog) Coupling(ch int) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cpl[ch&1]
}

// fuzzPanel answers the one hard button the web UI itself presses — AUTO
// (autoset). The synthetic "device autoset" restores a sane acquisition
// (RUN + the home timebase), which is exactly the observable contract the
// UI's convergence wait needs.
type fuzzPanel struct{ z *fuzzScope }

func (p *fuzzPanel) InjectButton(name string) bool {
	if name != "auto" {
		return false
	}
	p.z.mu.Lock()
	p.z.running, p.z.single = true, false
	p.z.tdiv = 500e-6
	p.z.mu.Unlock()
	return true
}
func (p *fuzzPanel) InjectKnob(name string, dir, steps int) bool { return false }

// TestFuzzBrowserSynthetic runs the seeded operator fuzzer (e2e/fuzz.mjs — the
// same driver used against the live scope) for a bounded, fixed-seed campaign
// against a local httptest server + the control-responsive synthetic engine
// above. Every finding the fuzzer records is a test failure with the
// findings.jsonl content in the message. Self-skips when node/Playwright is
// absent; a hard failure on the CI browser lane (testenv).
func TestFuzzBrowserSynthetic(t *testing.T) {
	testenv.NeedNode(t)
	fs := newFuzzScope()
	srv := httptest.NewServer(New(fs, newFuzzAnalog(), &fuzzPanel{z: fs}, nil).Handler())
	defer srv.Close()

	// Fixed ITERS/SEED so every CI run replays the identical campaign;
	// FUZZ_ITERS/FUZZ_SEED override for longer local hunts.
	iters, seed := "60", "1"
	if v := os.Getenv("FUZZ_ITERS"); v != "" {
		iters = v
	}
	if v := os.Getenv("FUZZ_SEED"); v != "" {
		seed = v
	}
	out := t.TempDir()
	cmd := exec.Command("node", "e2e/fuzz.mjs")
	cmd.Env = append(os.Environ(),
		"SCOPE_URL="+srv.URL,
		"ITERS="+iters,
		"SEED="+seed,
		"OUT="+out,
	)
	b, err := cmd.CombinedOutput()
	t.Logf("e2e/fuzz.mjs (synthetic scope):\n%s", b)
	if strings.HasPrefix(strings.TrimSpace(string(b)), "SKIP:") {
		testenv.SkipBrowser(t, "browser driver skipped: %s", firstLine(b))
	}
	findings, _ := os.ReadFile(filepath.Join(out, "findings.jsonl"))
	if len(findings) > 0 {
		t.Fatalf("operator fuzz found %d finding(s) against the synthetic scope — findings.jsonl:\n%s",
			strings.Count(string(findings), "\n"), findings)
	}
	if err != nil {
		t.Fatalf("fuzz driver failed without recording findings: %v", err)
	}
}

func (a *fuzzAnalog) TrigVolts(code uint16, srcCh int) float64   { return (31434 - float64(code)) / 938 }
func (a *fuzzAnalog) TrigCalActive(srcCh int) (float64, float64) { return 31434, 938 }
