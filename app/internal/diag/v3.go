// Schema v3 rungs of 06-TIERS §6 as diag jobs (all through the Runner, all
// restoring the engine's program):
//
//   - R1  GpmcSweep / GpmcApply / GpmcStatus — the CS1 timing sweep of
//     bus.Sweeper on a TSRC ramp, its persistence next to the app and the
//     ramp-gated apply (the same gate the boot path runs).
//   - R2  SchemaCheck — every non-stub v3 register reads back what was
//     written (N writes each, fields masked; IL_CTRL only its live fields),
//     every stub reads 0 after a write, 0x25 and 0x24 are independent, the
//     undecoded vendor words 0x21/0x57 change nothing, VERSION/build-ID match.
//   - R2b Capture with Tsrc (diag.go) — the drained words against
//     iface.TsrcCheck in both CHMODEs.
//   - R3  Redrain — full drain, REWIND re-drains, then windows of the same
//     frozen record: every window byte-identical to its slice, 0 breaks.
// ENGMODEL-OWNER-UNIT: FU-APP-DIAG
package diag

import (
	"fmt"
	"math/rand"
	"os"
	"strings"
	"sync"
	"time"

	"open-sds/app/internal/bus"
	"open-sds/app/internal/iface"
)

// ---- R1: GPMC timing ----

// timingCtl is the GPMC timing state the app hands the diag at boot.
// TRLC-LINKS: REQ-SDS-146
type timingCtl struct {
	port      bus.TimingPort
	path      string
	appVer    string
	boot      bus.BootTiming
	lastSweep *bus.SweepResult
	job       *sweepJob
}

// sweepJob owns a Sweeper and drives it one Step per Engine.Exec from its own
// goroutine. Nothing about it blocks the HTTP handler: the caller starts it
// and polls GpmcStatus. Each step restores the start timing before it returns
// (bus.Sweeper.Step), so no step can leave the controller at a candidate.
// TRLC-LINKS: REQ-SDS-146
type sweepJob struct {
	sw      *bus.Sweeper
	started time.Time
	mu      sync.Mutex
	steps   int
	done    bool
	err     error
}

// TRLC-LINKS: REQ-SDS-146
func (j *sweepJob) finished() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.done
}

// TRLC-LINKS: REQ-SDS-146
func (j *sweepJob) progress() *SweepProgress {
	j.mu.Lock()
	defer j.mu.Unlock()
	p := &SweepProgress{Running: !j.done, Steps: j.steps, Phase: j.sw.Phase(), Setting: j.sw.Setting(),
		Elapsed: time.Since(j.started).Seconds(), Finished: j.done}
	if j.err != nil {
		p.Err = j.err.Error()
	}
	return p
}

// SetTiming wires the CS1 timing port (nil when /dev/mem is unavailable: the
// sweep and apply then refuse), the persisted-timing path and what the boot
// hook decided.
// TRLC-LINKS: REQ-SDS-146
func (d *Diag) SetTiming(port bus.TimingPort, path, appVersion string, boot bus.BootTiming) {
	d.mu.Lock()
	d.timing = &timingCtl{port: port, path: path, appVer: appVersion, boot: boot}
	d.mu.Unlock()
}

// TRLC-LINKS: REQ-SDS-146
func (d *Diag) timingPort() (*timingCtl, error) {
	d.mu.Lock()
	tc := d.timing
	d.mu.Unlock()
	if tc == nil || tc.port == nil {
		return nil, fmt.Errorf("diag: no GPMC timing port (/dev/mem unavailable or the fabric was not verified)")
	}
	return tc, nil
}

// GpmcStatus is the R1 status view: the timing in force, the boot decision,
// the persisted file (if any) and the last sweep of this process.
// TRLC-LINKS: REQ-SDS-146
type GpmcStatus struct {
	Port       bool              `json:"port"` // the timing port is available
	Path       string            `json:"path"`
	Current    *bus.TimingFields `json:"current,omitempty"`
	CurrentRaw *bus.CS1Timing    `json:"current_raw,omitempty"`
	Boot       bus.BootTiming    `json:"boot"`
	Persisted  *bus.TimingFile   `json:"persisted,omitempty"`
	FileErr    string            `json:"file_err,omitempty"`
	LastSweep  *bus.SweepResult  `json:"last_sweep,omitempty"`
	Sweep      *SweepProgress    `json:"sweep,omitempty"`      // the running (or last) step-wise sweep
	Regions    []bus.CSRegion    `json:"cs_regions,omitempty"` // every GPMC chip-select region, read-only
	Err        string            `json:"err,omitempty"`
}

// SweepProgress is the R1 sweep job's live state. The sweep runs as a
// sequence of short Exec steps (05-WORKPLAN §4.3: one bounded piece per
// Exec, so the engine's beats advance and the agent's health token stays
// fresh); this is what /api/diag/gpmc reports while it does.
// TRLC-LINKS: REQ-SDS-146
type SweepProgress struct {
	Running  bool    `json:"running"`
	Steps    int     `json:"steps"`
	Phase    string  `json:"phase"`
	Setting  string  `json:"setting"`
	Elapsed  float64 `json:"elapsed_s"`
	Err      string  `json:"err,omitempty"`
	Finished bool    `json:"finished"`
}

// GpmcStatus reads the controller (no fabric traffic).
// TRLC-LINKS: REQ-SDS-146
func (d *Diag) GpmcStatus() GpmcStatus {
	d.mu.Lock()
	tc := d.timing
	d.mu.Unlock()
	st := GpmcStatus{}
	if tc == nil {
		st.Err = "timing not wired"
		return st
	}
	st.Path, st.Boot, st.LastSweep, st.Port = tc.path, tc.boot, tc.lastSweep, tc.port != nil
	if tc.job != nil {
		st.Sweep = tc.job.progress()
	}
	if rg, err := bus.SurveyCSRegions(); err == nil {
		st.Regions = rg
	}
	if tc.port != nil {
		if t, err := tc.port.Read(); err != nil {
			st.Err = err.Error()
		} else {
			f := t.Fields()
			st.Current, st.CurrentRaw = &f, &t
		}
	}
	if tc.path != "" {
		if f, err := bus.LoadTiming(tc.path); err != nil {
			if !os.IsNotExist(err) {
				st.FileErr = err.Error()
			}
		} else {
			st.Persisted = &f
		}
	}
	return st
}

// sweepTimeout bounds the Exec: every setting costs Blocks × 2 × Drains
// records (≤ 8 ms each at the factory timing), at most ~60 settings, plus the
// verify run.
// TRLC-LINKS: REQ-SDS-146
func sweepTimeout(o bus.SweepOptions) time.Duration {
	drains := max(o.Drains, 7)
	blocks := max(o.Blocks, 3)
	words := o.Words
	if words <= 0 {
		words = iface.PretrigMax
	}
	per := time.Duration(words) * 400 * time.Nanosecond // one drain at the factory rate, with margin
	n := 60*blocks*2*drains + (max(o.VerifyWords, 400000)/words+1)*2*blocks
	return time.Duration(n)*per + 30*time.Second
}

// GpmcSweep starts the R1 sweep as a background job and returns at once. The
// job runs ONE bus.Sweeper.Step per Engine.Exec (bounded by the sweeper's own
// StepTimeout) so the owner goroutine is never held for the whole sweep: the
// engine keeps beating, the app keeps writing its health token and the OTA
// agent does not terminate it mid-sweep (05-WORKPLAN §4.3). Every step
// restores the timing it started from, so an abort at any point — including a
// SIGTERM between steps — leaves the controller at the start timing, never at
// a candidate. Poll GpmcStatus for progress; the result lands in LastSweep.
// TRLC-LINKS: REQ-SDS-146
func (d *Diag) GpmcSweep(o bus.SweepOptions) (*SweepProgress, error) {
	tc, err := d.timingPort()
	if err != nil {
		return nil, err
	}
	d.mu.Lock()
	if tc.job != nil && !tc.job.finished() {
		d.mu.Unlock()
		return tc.job.progress(), fmt.Errorf("diag: a gpmc sweep is already running")
	}
	j := &sweepJob{sw: bus.NewSweeper(tc.port, o, d.logf), started: time.Now()}
	j.sw.Sleep = d.sleep
	tc.job = j
	d.mu.Unlock()
	go d.runSweep(tc, j)
	return j.progress(), nil
}

// runSweep is the job goroutine: Step until done, one Exec each.
// TRLC-LINKS: REQ-SDS-146
func (d *Diag) runSweep(tc *timingCtl, j *sweepJob) {
	for {
		var done bool
		err := d.run.Exec(func(b bus.Bus) error {
			snap, serr := saveRegs(b)
			if serr != nil {
				return serr
			}
			defer snap.restore(b)
			var e error
			done, e = j.sw.Step(b)
			return e
		}, j.sw.StepTimeout()+2*time.Second)
		j.mu.Lock()
		j.steps++
		if err != nil {
			j.err, j.done = err, true
		} else if done {
			j.done = true
		}
		fin := j.done
		j.mu.Unlock()
		if fin {
			break
		}
	}
	res := j.sw.Result()
	d.mu.Lock()
	if res != nil {
		tc.lastSweep = res
	}
	d.mu.Unlock()
	if res != nil {
		d.logf("gpmc sweep: done in %d steps, %.0f s, ok=%v chosen=%v", j.steps, time.Since(j.started).Seconds(), res.OK, res.Chosen)
	}
}

// GpmcPersist writes the last passed sweep's timing to the file next to the
// app. It does not apply it: GpmcApply("persisted") (or the next boot) runs
// the ramp gate first.
// TRLC-LINKS: REQ-SDS-146
func (d *Diag) GpmcPersist(appVersion string) (bus.TimingFile, error) {
	d.mu.Lock()
	tc := d.timing
	d.mu.Unlock()
	if tc == nil || tc.lastSweep == nil {
		return bus.TimingFile{}, fmt.Errorf("diag: no sweep result to persist — run the sweep first")
	}
	f, err := bus.FileFromSweep(tc.lastSweep, appVersion)
	if err != nil {
		return f, err
	}
	if err := bus.SaveTiming(tc.path, f); err != nil {
		return f, err
	}
	d.logf("gpmc timing: persisted %s to %s", f.Chosen, tc.path)
	return f, nil
}

// GpmcApply applies "persisted" (the file, through the boot gate: ramp check
// at the persisted timing, factory on failure) or "factory" (the timing the
// boot hook read). The result is the same shape the boot log carries.
// TRLC-LINKS: REQ-SDS-146
func (d *Diag) GpmcApply(which string) (bus.BootTiming, error) {
	tc, err := d.timingPort()
	if err != nil {
		return bus.BootTiming{}, err
	}
	var bt bus.BootTiming
	err = d.run.Exec(func(b bus.Bus) error {
		snap, err := saveRegs(b)
		if err != nil {
			return err
		}
		defer snap.restore(b)
		switch strings.ToLower(which) {
		case "persisted", "":
			bt = bus.ApplyPersistedTiming(b, tc.port, tc.path, tc.appVer, d.logf)
		case "factory":
			if tc.boot.Factory.RdCycle == 0 {
				return fmt.Errorf("diag: the boot hook recorded no factory timing")
			}
			factory, err := tc.port.Read()
			if err != nil {
				return err
			}
			if err := tc.port.Restore(fromFields(tc.boot.Factory, factory)); err != nil {
				return err
			}
			cur, _ := tc.port.Read()
			bt = bus.BootTiming{Path: tc.path, Source: "factory", Factory: tc.boot.Factory, Applied: cur.Fields()}
			d.logf("gpmc timing: factory timing restored by request (%s)", cur)
		default:
			return fmt.Errorf("diag: apply %q: want persisted or factory", which)
		}
		return nil
	}, 30*time.Second)
	if err == nil {
		d.mu.Lock()
		tc.boot = bt
		d.mu.Unlock()
	}
	return bt, err
}

// fromFields rebuilds the factory read-side timing from the decoded fields
// the boot hook kept, on top of the controller's current words (the write
// side and CONFIG1/3/7 are whatever is in force — they were never changed).
// TRLC-LINKS: REQ-SDS-146
func fromFields(f bus.TimingFields, cur bus.CS1Timing) bus.CS1Timing {
	return cur.WithRdAccess(f.RdAccess).WithRdCycle(f.RdCycle).WithOEOn(f.OEOn).WithOEOff(f.OEOff).
		WithCSRdOff(f.CSRdOff).WithGap(f.Gap)
}

// ---- R2: schema check ----

// SchemaOptions tunes the register check.
// TRLC-LINKS: REQ-SDS-147
type SchemaOptions struct {
	Writes int   `json:"writes"` // writes per register (default 100)
	Seed   int64 `json:"seed"`   // PRNG seed (default 1)
}

// RegCheck is one register's verdict.
// TRLC-LINKS: REQ-SDS-147
type RegCheck struct {
	Name       string `json:"name"`
	Sel        uint16 `json:"sel"`
	Stub       bool   `json:"stub"`
	LiveMask   uint16 `json:"live_mask"` // bits expected to read back (0 for a stub)
	Writes     int    `json:"writes"`
	Mismatches int    `json:"mismatches"`
	Wrote      uint16 `json:"first_wrote,omitempty"`
	Read       uint16 `json:"first_read,omitempty"`
	Expected   uint16 `json:"first_expected,omitempty"`
	OK         bool   `json:"ok"`
}

// SchemaResult is the R2 report.
// TRLC-LINKS: REQ-SDS-147
type SchemaResult struct {
	Identity     Identity          `json:"identity"`
	Registers    []RegCheck        `json:"registers"`   // every writable v3 register (stubs included)
	StubReads    map[string]uint16 `json:"stub_reads"`  // read-only stubs: must read 0
	DiagStubs    []RegCheck        `json:"diag_stubs"`  // writable DIAG stubs: write ignored, reads 0
	Lanemap      []uint16          `json:"lanemap"`     // the baked map as reported (read-only)
	LanemapRO    bool              `json:"lanemap_ro"`  // a write to LANEMAP.0 did not change it
	Independent  bool              `json:"independent"` // IL_CTRL (0x25) and RUN (0x24) do not alias
	IndepDetail  string            `json:"indep_detail,omitempty"`
	Undecoded    map[string]uint16 `json:"undecoded"`    // 0x21 / 0x57 read values (must be 0)
	UndecodedOK  bool              `json:"undecoded_ok"` // the vendor words changed no register
	UndecodedDif []string          `json:"undecoded_changed,omitempty"`
	Restored     bool              `json:"restored"`
	OK           bool              `json:"ok"`
}

// liveMask is the union of a register's fields that the v2.2 fabric stores
// (fields whose Desc carries the "reads 0 in v2.2" mark are masked by the
// fabric); a register without fields is fully live.
// TRLC-LINKS: REQ-SDS-147
func liveMask(r iface.Register) uint16 {
	if r.Stub {
		return 0
	}
	if len(r.Fields) == 0 {
		return 0xffff
	}
	var m uint16
	for _, f := range r.Fields {
		if strings.Contains(f.Desc, "reads 0 in v2.2") {
			continue
		}
		m |= f.Mask
	}
	return m
}

// TRLC-LINKS: REQ-SDS-147
func fieldUnion(fs []iface.Field) uint16 {
	if len(fs) == 0 {
		return 0xffff
	}
	var m uint16
	for _, f := range fs {
		m |= f.Mask
	}
	return m
}

// SchemaCheck runs R2. Only v3 registers are written (random values masked
// to their fields); the v2 program is saved and restored around the run.
// TRLC-LINKS: REQ-SDS-147
func (d *Diag) SchemaCheck(o SchemaOptions) (*SchemaResult, error) {
	if o.Writes <= 0 {
		o.Writes = 100
	}
	if o.Seed == 0 {
		o.Seed = 1
	}
	rng := rand.New(rand.NewSource(o.Seed))
	res := &SchemaResult{StubReads: map[string]uint16{}, Undecoded: map[string]uint16{}, OK: true}
	err := d.run.Exec(func(b bus.Bus) error {
		snap, err := saveRegs(b)
		if err != nil {
			return err
		}
		defer func() { res.Restored = snap.restore(b) == nil }()
		if err := b.Write(bus.PlaneCS1, iface.SelOpcode, iface.OpReset); err != nil {
			return err
		}
		if res.Identity, err = readIdentity(b); err != nil {
			return err
		}
		if !res.Identity.OK {
			res.OK = false
		}
		// Writable v3 registers, stubs included.
		for _, r := range iface.Registers() {
			if r.Since < 3 || !r.Access.CanWrite() || r.Strobe {
				continue
			}
			rc := RegCheck{Name: r.Name, Sel: r.Sel, Stub: r.Stub, LiveMask: liveMask(r), OK: true}
			union := fieldUnion(r.Fields)
			orig, _ := b.Read(bus.PlaneCS1, r.Sel)
			for i := 0; i < o.Writes; i++ {
				v := uint16(rng.Intn(0x10000)) & union
				if err := b.Write(bus.PlaneCS1, r.Sel, v); err != nil {
					return err
				}
				got, err := b.Read(bus.PlaneCS1, r.Sel)
				if err != nil {
					return err
				}
				want := v & rc.LiveMask
				rc.Writes++
				if got != want {
					if rc.Mismatches == 0 {
						rc.Wrote, rc.Read, rc.Expected = v, got, want
					}
					rc.Mismatches++
					rc.OK = false
				}
			}
			_ = b.Write(bus.PlaneCS1, r.Sel, orig&rc.LiveMask)
			if !rc.OK {
				res.OK = false
			}
			res.Registers = append(res.Registers, rc)
		}
		// Read-only stubs read 0.
		for _, r := range iface.Registers() {
			if !r.Stub || r.Access.CanWrite() {
				continue
			}
			v, _ := b.Read(bus.PlaneCS1, r.Sel)
			res.StubReads[r.Name] = v
			if v != 0 {
				res.OK = false
			}
		}
		// DIAG stubs: a write is ignored, the entry reads 0.
		for _, e := range iface.DiagWindow() {
			if !e.Stub {
				continue
			}
			rc := RegCheck{Name: e.Name, Sel: e.Idx, Stub: true, OK: true}
			for i := 0; i < e.Count; i++ {
				idx := e.Idx + uint16(i)
				if e.Access.CanWrite() {
					if err := winWrite(b, idx, 0xffff); err != nil {
						return err
					}
					rc.Writes++
				}
				v, _ := winRead(b, idx)
				if v != 0 {
					if rc.Mismatches == 0 {
						rc.Wrote, rc.Read = 0xffff, v
					}
					rc.Mismatches++
					rc.OK = false
					res.OK = false
				}
			}
			res.DiagStubs = append(res.DiagStubs, rc)
		}
		// LANEMAP reports the baked map and ignores writes.
		for i := 0; i < iface.DiagLanemapCount; i++ {
			v, _ := winRead(b, iface.DiagLanemapBase+uint16(i))
			res.Lanemap = append(res.Lanemap, v)
		}
		before := res.Lanemap[0]
		if err := b.Write(bus.PlaneCS1, iface.SelDiagIdx, iface.DiagLanemapBase); err != nil {
			return err
		}
		_ = b.Write(bus.PlaneCS1, iface.SelDiagData, before^0x7f)
		after, _ := winRead(b, iface.DiagLanemapBase)
		res.LanemapRO = after == before
		if !res.LanemapRO {
			res.OK = false
		}
		// 0x25 vs 0x24: writing one leaves the other alone (IL_CTRL's live bits only).
		il := iface.IlCtrlTsrcColtag << iface.IlCtrlTsrcShift
		run := iface.RunChmodeCh2 << iface.RunChmodeShift // RUN idle, CHMODE 2
		_ = b.Write(bus.PlaneCS1, iface.SelRun, run)
		_ = b.Write(bus.PlaneCS1, iface.SelIlCtrl, il)
		r1, _ := b.Read(bus.PlaneCS1, iface.SelRun)
		i1, _ := b.Read(bus.PlaneCS1, iface.SelIlCtrl)
		_ = b.Write(bus.PlaneCS1, iface.SelRun, iface.RunChmodeCh1<<iface.RunChmodeShift)
		i2, _ := b.Read(bus.PlaneCS1, iface.SelIlCtrl)
		_ = b.Write(bus.PlaneCS1, iface.SelIlCtrl, 0)
		r2, _ := b.Read(bus.PlaneCS1, iface.SelRun)
		res.Independent = r1 == run && i1 == il && i2 == il && r2 == iface.RunChmodeCh1<<iface.RunChmodeShift
		if !res.Independent {
			res.IndepDetail = fmt.Sprintf("RUN=%#04x IL_CTRL=%#04x after writes; IL_CTRL=%#04x after RUN write; RUN=%#04x after IL_CTRL write", r1, i1, i2, r2)
			res.OK = false
		}
		// The undecoded vendor words: read 0, change nothing.
		rw := func() map[uint16]uint16 {
			m := map[uint16]uint16{}
			for _, r := range iface.Registers() {
				if r.Access == iface.AccRW && !r.Pop {
					m[r.Sel], _ = b.Read(bus.PlaneCS1, r.Sel)
				}
			}
			return m
		}
		pre := rw()
		for _, u := range iface.Undecoded() {
			for _, w := range vendorWords {
				if w.sel == u {
					if err := b.RawWrite(u, w.val); err != nil {
						return err
					}
				}
			}
			_ = b.RawWrite(u, 0xffff)
			v, _ := b.Read(bus.PlaneCS1, u)
			res.Undecoded[fmt.Sprintf("0x%02x", u)] = v
			if v != 0 {
				res.OK = false
			}
		}
		post := rw()
		res.UndecodedOK = true
		for sel, v := range pre {
			if post[sel] != v {
				r, _ := iface.BySel(sel)
				res.UndecodedDif = append(res.UndecodedDif, fmt.Sprintf("%s %#04x→%#04x", r.Name, v, post[sel]))
				res.UndecodedOK = false
				res.OK = false
			}
		}
		return nil
	}, d.timeout+time.Duration(o.Writes)*20*time.Millisecond)
	if err != nil {
		return nil, err
	}
	return res, nil
}

// ---- R3: windowed re-drains ----

// RedrainOptions tunes R3.
// TRLC-LINKS: REQ-SDS-148
type RedrainOptions struct {
	Words   int    `json:"words"`   // record words (default iface.PretrigMax)
	Tsrc    uint16 `json:"tsrc"`    // test source (default RAMP)
	Adc     bool   `json:"adc"`     // the converters instead of a test source: only byte identity is checked
	Chmode  uint16 `json:"chmode"`  // RUN.CHMODE
	Passes  int    `json:"passes"`  // full REWIND re-drains (default 2)
	Windows int    `json:"windows"` // evenly spaced windows (default 8)
	Odd     bool   `json:"odd"`     // add an unaligned window (start 3, len 7) and the last-word window
}

// WindowCheck is one window's verdict.
// TRLC-LINKS: REQ-SDS-148
type WindowCheck struct {
	Window    bus.Window    `json:"window"`
	Exact     bus.Exactness `json:"exact"`
	NsPerWord float64       `json:"ns_per_word"`
}

// RedrainResult is the R3 report.
// TRLC-LINKS: REQ-SDS-148
type RedrainResult struct {
	Tsrc      uint16        `json:"tsrc"`
	RecLen    int           `json:"rec_len"`
	Full      bus.Exactness `json:"full"` // the reference drain, pattern-checked
	FullNs    float64       `json:"full_ns_per_word"`
	Passes    []WindowCheck `json:"passes"`  // full re-drains vs the reference
	Windows   []WindowCheck `json:"windows"` // windows vs their slices
	Bad       int           `json:"bad"`     // drains that were not exact
	Breaks    int           `json:"breaks"`
	Mismatch  int           `json:"mismatch"`
	Underruns int           `json:"underruns"`
	WordsPerS float64       `json:"words_per_s"`
	Restored  bool          `json:"restored"`
	OK        bool          `json:"ok"`
}

// Redrain runs R3: a frozen test-source record drained in full, re-drained
// through REWIND, then read through windows — every window must equal its
// slice of the full drain and follow the pattern; pops and underruns must
// account exactly.
// TRLC-LINKS: REQ-SDS-148
func (d *Diag) Redrain(o RedrainOptions) (*RedrainResult, error) {
	if o.Words <= 0 || o.Words > iface.PretrigMax {
		o.Words = iface.PretrigMax
	}
	if o.Passes <= 0 {
		o.Passes = 2
	}
	if o.Windows <= 0 {
		o.Windows = 8
	}
	if o.Tsrc == 0 {
		o.Tsrc = iface.IlCtrlTsrcRamp
	}
	if o.Adc {
		o.Tsrc = iface.IlCtrlTsrcAdc
	}
	if o.Tsrc > iface.IlCtrlTsrcGlitch || o.Chmode > iface.RunChmodeCh2 {
		return nil, fmt.Errorf("diag: tsrc %d / chmode %d out of range", o.Tsrc, o.Chmode)
	}
	res := &RedrainResult{Tsrc: o.Tsrc, OK: true}
	err := d.run.Exec(func(b bus.Bus) error {
		snap, err := saveRegs(b)
		if err != nil {
			return err
		}
		defer func() { res.Restored = snap.restore(b) == nil }()
		rec, err := bus.CaptureTsrc(b, bus.TsrcOptions{Words: o.Words, Tsrc: o.Tsrc, Chmode: o.Chmode}, d.sleep)
		if err != nil {
			return err
		}
		res.RecLen = rec
		ref := make([]uint16, rec)
		r, err := bus.DrainWindow(b, bus.Full, ref)
		if err != nil {
			return err
		}
		ref = ref[:r.N]
		res.Full = bus.CheckExact(r, ref, o.Tsrc, nil)
		res.FullNs = r.NsPerWord()
		if res.FullNs > 0 {
			res.WordsPerS = 1e9 / res.FullNs
		}
		tally := func(e bus.Exactness) {
			res.Breaks += e.Breaks
			res.Mismatch += e.Mismatch
			res.Underruns += e.Underruns
			if !e.OK {
				res.Bad++
				res.OK = false
			}
		}
		tally(res.Full)
		buf := make([]uint16, rec)
		for p := 0; p < o.Passes; p++ {
			r, err := bus.DrainWindow(b, bus.Full, buf)
			if err != nil {
				return err
			}
			e := bus.CheckExact(r, buf[:r.N], o.Tsrc, ref)
			tally(e)
			res.Passes = append(res.Passes, WindowCheck{Window: bus.Full, Exact: e, NsPerWord: r.NsPerWord()})
		}
		wins := make([]bus.Window, 0, o.Windows+2)
		step := rec / o.Windows
		if step < 1 {
			step = 1
		}
		for i := 0; i < o.Windows && i*step < rec; i++ {
			l := step
			if i == o.Windows-1 {
				l = 0 // the last window runs to the end (LEN 0)
			}
			wins = append(wins, bus.Window{Start: uint16(i * step), Len: uint16(l)})
		}
		if o.Odd && rec > 10 {
			wins = append(wins, bus.Window{Start: 3, Len: 7}, bus.Window{Start: uint16(rec - 1), Len: 1})
		}
		for _, w := range wins {
			end := rec
			if w.Len != 0 && int(w.Start)+int(w.Len) < rec {
				end = int(w.Start) + int(w.Len)
			}
			r, err := bus.DrainWindow(b, w, buf[:end-int(w.Start)])
			if err != nil {
				return err
			}
			e := bus.CheckExact(r, buf[:r.N], o.Tsrc, ref[w.Start:end])
			tally(e)
			res.Windows = append(res.Windows, WindowCheck{Window: w, Exact: e, NsPerWord: r.NsPerWord()})
		}
		return nil
	}, d.timeout+time.Duration(o.Passes+o.Windows+2)*20*time.Millisecond)
	if err != nil {
		return nil, err
	}
	return res, nil
}
