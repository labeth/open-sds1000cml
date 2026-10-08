// Package diag is the app-side driver of the default image's diagnostic block
// (workplan §2 DIAG_CTRL / DIAG window, §4.7 experiment rules, 04-DESIGN §4):
// read/write any selector or DIAG window by name, the lane/bus census, the
// snapshot RAM, per-ball bus drive, the vendor CS1 word sequence (E1) and the
// bus-ownership experiment (E2), plus a status line for health. The schema v3
// rungs of 06-TIERS §6 live in v3.go: the GPMC timing sweep (R1), the
// register check (R2), the test-source captures (R2b) and the windowed
// re-drain check (R3).
//
// Every fabric access goes through a Runner — the engine's Exec — so the bus
// keeps its single owner. The package holds no bus state of its own: each call
// reads what it needs, and every experiment restores the registers it touched.
// ENGMODEL-OWNER-UNIT: FU-APP-DIAG
package diag

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"open-sds/app/internal/bus"
	"open-sds/app/internal/iface"
)

// Runner executes fn with the bus on the goroutine that owns it.
// TRLC-LINKS: REQ-SDS-143
type Runner interface {
	Exec(fn func(bus.Bus) error, timeout time.Duration) error
}

// RunnerFunc adapts a function to Runner.
// TRLC-LINKS: REQ-SDS-143
type RunnerFunc func(fn func(bus.Bus) error, timeout time.Duration) error

// TRLC-LINKS: REQ-SDS-143
func (f RunnerFunc) Exec(fn func(bus.Bus) error, timeout time.Duration) error { return f(fn, timeout) }

// Diag drives the diagnostic block.
// TRLC-LINKS: REQ-SDS-143
type Diag struct {
	run     Runner
	logf    func(string, ...any)
	timeout time.Duration
	sleep   func(time.Duration) // inside Exec, on the owner goroutine

	mu       sync.Mutex
	lastCens *Census
	censAt   time.Time
	extra    func() map[string]any // host-side status fields (engine stats, heartbeat mode)
	timing   *timingCtl            // GPMC CS1 timing port + persistence (v3.go), nil until SetTiming
}

// New creates a Diag over the runner. logf may be nil.
// laneTogSettle / busSettle are the hardware settle times used by Census and E2
// (overridable in tests through the Diag.sleep hook).
const (
	laneTogSettle = 3 * time.Millisecond
	busSettle     = 1 * time.Millisecond
)

// TRLC-LINKS: REQ-SDS-143
func New(run Runner, logf func(string, ...any)) *Diag {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Diag{run: run, logf: logf, timeout: 5 * time.Second, sleep: time.Sleep}
}

// SetStatusExtra installs a provider of host-side status fields merged into
// Status() (engine frames, health heartbeat mode, EDMA state).
// TRLC-LINKS: REQ-SDS-143
func (d *Diag) SetStatusExtra(fn func() map[string]any) {
	d.mu.Lock()
	d.extra = fn
	d.mu.Unlock()
}

// ---- ball tables (01-CONTRACT §3/§4, workplan §2.1 index order) ----

// BusBalls is the 27-ball OE-gated bus in DIAG BUS_* bit order (0..15 in the
// LO word, 16..26 in the HI word).
var BusBalls = []string{
	"J6", "K5", "L1", "L2", "L3", "L4", "N1", "N2", "P1", "P2", "R1", "M6", "N6", "N3", "N5", "P3",
	"R3", "R4", "R5", "R6", "R7", "T2", "T3", "T4", "T5", "T6", "T7",
}

// SingleBalls are the five individually-enabled bidirectionals (SINGLE_CTRL bit order).
var SingleBalls = []string{"F3", "F5", "G5", "D3", "F7"}

// adcHoldBalls are the bus members the proven ADC recipe holds static 1
// (DIAG ADC_HOLD bits 0..2).
var adcHoldBalls = map[string]uint16{"L4": iface.DiagAdcHoldL4Mask, "T2": iface.DiagAdcHoldT2Mask, "T7": iface.DiagAdcHoldT7Mask}

// BusBallIndex returns the DIAG bit index of a bus ball.
// TRLC-LINKS: REQ-SDS-144, REQ-SDS-145
func BusBallIndex(ball string) (int, bool) {
	for i, b := range BusBalls {
		if strings.EqualFold(b, ball) {
			return i, true
		}
	}
	return 0, false
}

// LaneName names a LANE_IDX value (DIAG LANE_IDX field doc).
// TRLC-LINKS: REQ-SDS-144
func LaneName(idx int) string {
	switch {
	case idx >= 0 && idx < 80:
		return fmt.Sprintf("lane%02d", idx)
	case idx >= 0x50 && idx < 0x50+len(BusBalls):
		return BusBalls[idx-0x50]
	case idx >= 0x70 && idx < 0x70+len(SingleBalls):
		return SingleBalls[idx-0x70]
	case idx == 0x78:
		return "P6"
	case idx == 0x79:
		return "A2"
	case idx == 0x7a:
		return "B1"
	case idx == 0x7b:
		return "K2"
	case idx == 0x7c:
		return "B11"
	case idx == 0x7d:
		return "J1"
	}
	return ""
}

// laneIndices lists every valid LANE_IDX value in order.
// TRLC-LINKS: REQ-SDS-144
func laneIndices() []int {
	var out []int
	for i := 0; i < 0x7e; i++ {
		if LaneName(i) != "" {
			out = append(out, i)
		}
	}
	return out
}

// ---- register access by name ----

// ResolveSel maps a register name or a hex/decimal selector to a selector.
// TRLC-LINKS: REQ-SDS-143
func ResolveSel(name string) (uint16, iface.Register, error) {
	if r, ok := iface.ByName(strings.ToUpper(name)); ok {
		return r.Sel, r, nil
	}
	if v, err := strconv.ParseUint(strings.TrimPrefix(strings.ToLower(name), "0x"), 16, 16); err == nil && strings.HasPrefix(strings.ToLower(name), "0x") {
		r, _ := iface.BySel(uint16(v))
		return uint16(v), r, nil
	}
	if v, err := strconv.ParseUint(name, 0, 16); err == nil {
		r, _ := iface.BySel(uint16(v))
		return uint16(v), r, nil
	}
	return 0, iface.Register{}, fmt.Errorf("diag: unknown register %q", name)
}

// ResolveDiagIdx maps a DIAG window name (LANEMAP[i] as "LANEMAP.12") or index.
// TRLC-LINKS: REQ-SDS-143
func ResolveDiagIdx(name string) (uint16, iface.DiagEntry, error) {
	up := strings.ToUpper(name)
	for _, e := range iface.DiagWindow() {
		if e.Name == up {
			return e.Idx, e, nil
		}
		if e.Count > 1 && strings.HasPrefix(up, e.Name+".") {
			i, err := strconv.Atoi(strings.TrimPrefix(up, e.Name+"."))
			if err == nil && i >= 0 && i < e.Count {
				return e.Idx + uint16(i), e, nil
			}
		}
	}
	if v, err := strconv.ParseUint(name, 0, 16); err == nil && v < 0x100 {
		return uint16(v), diagEntryAt(uint16(v)), nil
	}
	return 0, iface.DiagEntry{}, fmt.Errorf("diag: unknown DIAG window %q", name)
}

// TRLC-LINKS: REQ-SDS-143
func diagEntryAt(idx uint16) iface.DiagEntry {
	for _, e := range iface.DiagWindow() {
		if idx >= e.Idx && int(idx) < int(e.Idx)+e.Count {
			return e
		}
	}
	return iface.DiagEntry{}
}

// RegVal is one register read with its decoded fields.
// TRLC-LINKS: REQ-SDS-143
type RegVal struct {
	Name   string            `json:"name"`
	Sel    uint16            `json:"sel"`
	Value  uint16            `json:"value"`
	Hex    string            `json:"hex"`
	Fields map[string]uint16 `json:"fields,omitempty"`
	Access string            `json:"access,omitempty"`
}

// TRLC-LINKS: REQ-SDS-143
func decode(r iface.Register, v uint16) RegVal {
	rv := RegVal{Name: r.Name, Sel: r.Sel, Value: v, Hex: fmt.Sprintf("0x%04x", v), Access: r.Access.String()}
	if len(r.Fields) > 0 {
		rv.Fields = map[string]uint16{}
		for _, f := range r.Fields {
			rv.Fields[f.Name] = f.Get(v)
		}
	}
	if r.Pop {
		rv.Access += " pop"
	}
	return rv
}

// RegRead reads one CS1 register by name or selector. A pop-on-read port pops
// one word (the caller asked for it).
// TRLC-LINKS: REQ-SDS-143
func (d *Diag) RegRead(name string) (RegVal, error) {
	sel, r, err := ResolveSel(name)
	if err != nil {
		return RegVal{}, err
	}
	var v uint16
	err = d.run.Exec(func(b bus.Bus) error {
		var e error
		v, e = b.Read(bus.PlaneCS1, sel)
		return e
	}, d.timeout)
	if err != nil {
		return RegVal{}, err
	}
	rv := decode(r, v)
	rv.Sel = sel
	if rv.Name == "" {
		rv.Name = fmt.Sprintf("0x%02x", sel)
	}
	return rv, nil
}

// RegWrite writes one CS1 register. raw bypasses the schema guard (the
// vendor-word path); otherwise a read-only register is refused.
// TRLC-LINKS: REQ-SDS-143
func (d *Diag) RegWrite(name string, val uint16, raw bool) error {
	_, err := d.RegWriteSnoop(name, val, raw)
	return err
}

// SnoopVal is what the fabric's GPMC snoop registers recorded for the last
// CS1 write: the raw selector, the A2/B1 pad levels sampled at that write and
// the data word (05-WORKPLAN §2 SNOOP_SEL/SNOOP_DATA).
// TRLC-LINKS: REQ-SDS-143
type SnoopVal struct {
	Sel   uint16 `json:"sel"`
	A2    uint8  `json:"a2"`
	B1    uint8  `json:"b1"`
	Count uint16 `json:"count"`
	Data  uint16 `json:"data"`
}

// RegWriteSnoop writes a register and reads the snoop registers in the SAME
// engine transaction, so the engine's own per-frame writes cannot land in
// between (they did on hardware, 2026-09-05: SNOOP_SEL always showed the
// engine's OPCODE write). This is the primitive of the GPMC A1/A2 ball test.
// TRLC-LINKS: REQ-SDS-143
func (d *Diag) RegWriteSnoop(name string, val uint16, raw bool) (SnoopVal, error) {
	sel, _, err := ResolveSel(name)
	if err != nil {
		return SnoopVal{}, err
	}
	var sv SnoopVal
	err = d.run.Exec(func(b bus.Bus) error {
		var e error
		if raw {
			e = b.RawWrite(sel, val)
		} else {
			e = b.Write(bus.PlaneCS1, sel, val)
		}
		if e != nil {
			return e
		}
		s, e := b.Read(bus.PlaneCS1, iface.SelSnoopSel)
		if e != nil {
			return e
		}
		dw, e := b.Read(bus.PlaneCS1, iface.SelSnoopData)
		if e != nil {
			return e
		}
		sv = SnoopVal{Sel: s & 0x7f, A2: uint8(s >> 7 & 1), B1: uint8(s >> 8 & 1), Count: s >> 9, Data: dw}
		return nil
	}, d.timeout)
	return sv, err
}

// ---- DIAG window ----

// TRLC-LINKS: REQ-SDS-143
func winRead(b bus.Bus, idx uint16) (uint16, error) {
	if err := b.Write(bus.PlaneCS1, iface.SelDiagIdx, idx); err != nil {
		return 0, err
	}
	return b.Read(bus.PlaneCS1, iface.SelDiagData)
}

// TRLC-LINKS: REQ-SDS-143
func winWrite(b bus.Bus, idx, val uint16) error {
	if err := b.Write(bus.PlaneCS1, iface.SelDiagIdx, idx); err != nil {
		return err
	}
	return b.Write(bus.PlaneCS1, iface.SelDiagData, val)
}

// WindowVal is one DIAG window read.
// TRLC-LINKS: REQ-SDS-143
type WindowVal struct {
	Name   string            `json:"name"`
	Idx    uint16            `json:"idx"`
	Value  uint16            `json:"value"`
	Hex    string            `json:"hex"`
	Fields map[string]uint16 `json:"fields,omitempty"`
}

// WindowRead reads a DIAG window entry by name or index.
// TRLC-LINKS: REQ-SDS-143
func (d *Diag) WindowRead(name string) (WindowVal, error) {
	idx, e, err := ResolveDiagIdx(name)
	if err != nil {
		return WindowVal{}, err
	}
	var v uint16
	err = d.run.Exec(func(b bus.Bus) error {
		var er error
		v, er = winRead(b, idx)
		return er
	}, d.timeout)
	if err != nil {
		return WindowVal{}, err
	}
	wv := WindowVal{Name: e.Name, Idx: idx, Value: v, Hex: fmt.Sprintf("0x%04x", v)}
	if e.Count > 1 {
		wv.Name = fmt.Sprintf("%s.%d", e.Name, idx-e.Idx)
	}
	if len(e.Fields) > 0 {
		wv.Fields = map[string]uint16{}
		for _, f := range e.Fields {
			wv.Fields[f.Name] = f.Get(v)
		}
	}
	return wv, nil
}

// WindowWrite writes a DIAG window entry; read-only entries are refused —
// including LANEMAP, which reports the baked ten-core map from v2.2 on (a
// different map is a rebuild, 06-TIERS §1.7).
// TRLC-LINKS: REQ-SDS-143
func (d *Diag) WindowWrite(name string, val uint16) error {
	idx, e, err := ResolveDiagIdx(name)
	if err != nil {
		return err
	}
	if e.Name != "" && !e.Access.CanWrite() {
		if e.Name == "LANEMAP" {
			return fmt.Errorf("diag: DIAG LANEMAP is read-only in v3 (the baked map from fpga/default/lanemap_seed.vh; a different map is a rebuild)")
		}
		if e.Stub {
			return fmt.Errorf("diag: DIAG %s is read-only (%s)", e.Name, e.Desc)
		}
		return fmt.Errorf("diag: DIAG %s is read-only", e.Name)
	}
	return d.run.Exec(func(b bus.Bus) error { return winWrite(b, idx, val) }, d.timeout)
}

// ---- identity / status ----

// Identity is the fabric identity plus the configuration port.
// TRLC-LINKS: REQ-SDS-143
type Identity struct {
	BuildID  string `json:"build_id"`
	Version  string `json:"version"`
	FabricID string `json:"fabric_id"`
	Want     string `json:"want_build_id"`
	OK       bool   `json:"ok"`
	Err      string `json:"err,omitempty"`
	ConfPort string `json:"conf_port"` // CS3 0x07: 0x00c0 = configured
	ConfDone bool   `json:"conf_done"`
}

// TRLC-LINKS: REQ-SDS-143
func readIdentity(b bus.Bus) (Identity, error) {
	lo, err := b.Read(bus.PlaneCS1, iface.SelBuildidLo)
	if err != nil {
		return Identity{}, err
	}
	hi, _ := b.Read(bus.PlaneCS1, iface.SelBuildidHi)
	ver, _ := b.Read(bus.PlaneCS1, iface.SelVersion)
	fab, _ := b.Read(bus.PlaneCS1, iface.SelFabricId)
	cp, cerr := b.Read(bus.PlaneCS3, bus.CS3ConfigPort)
	id := Identity{
		BuildID:  fmt.Sprintf("0x%04x%04x", hi, lo),
		Version:  fmt.Sprintf("0x%04x", ver),
		FabricID: fmt.Sprintf("0x%04x", fab),
		Want:     fmt.Sprintf("0x%08x", iface.BuildID),
		ConfPort: fmt.Sprintf("0x%04x", cp),
		ConfDone: cerr == nil && cp&0x80 != 0,
	}
	if err := iface.CheckIdentity(lo, hi, ver, fab); err != nil {
		id.Err = err.Error()
	} else {
		id.OK = true
	}
	return id, nil
}

// Identity reads the identity words and the configuration port.
// TRLC-LINKS: REQ-SDS-143
func (d *Diag) Identity() (Identity, error) {
	var id Identity
	err := d.run.Exec(func(b bus.Bus) error {
		var e error
		id, e = readIdentity(b)
		return e
	}, d.timeout)
	return id, err
}

// Status is the one-line health view: identity, clocks, the last census
// summary and the host-side extras.
// TRLC-LINKS: REQ-SDS-143
func (d *Diag) Status() map[string]any {
	out := map[string]any{}
	var id Identity
	var clk uint16
	err := d.run.Exec(func(b bus.Bus) error {
		var e error
		id, e = readIdentity(b)
		if e != nil {
			return e
		}
		clk, e = b.Read(bus.PlaneCS1, iface.SelClkStat)
		return e
	}, d.timeout)
	if err != nil {
		out["err"] = err.Error()
	}
	out["identity"] = id
	out["pll_a_lock"] = clk&iface.ClkStatPllaLockMask != 0
	out["pll_b_lock"] = clk&iface.ClkStatPllbLockMask != 0
	out["m2_c2_ratio_x16"] = (clk & iface.ClkStatRatioMask) >> iface.ClkStatRatioShift
	d.mu.Lock()
	if d.lastCens != nil {
		out["census_at"] = d.censAt.Format(time.RFC3339)
		out["lanes_toggling"] = d.lastCens.LanesToggling
	}
	extra := d.extra
	d.mu.Unlock()
	if extra != nil {
		for k, v := range extra() {
			out[k] = v
		}
	}
	line := fmt.Sprintf("fabric=%s conf=%s plla=%v pllb=%v", boolWord(id.OK, "ok", "MISMATCH"), id.ConfPort,
		out["pll_a_lock"], out["pll_b_lock"])
	if !id.OK && id.Err != "" {
		line += " (" + id.Err + ")"
	}
	out["line"] = line
	return out
}

// TRLC-LINKS: REQ-SDS-143
func boolWord(b bool, t, f string) string {
	if b {
		return t
	}
	return f
}

// ---- census ----

// Lane is one input lane's counters.
// TRLC-LINKS: REQ-SDS-144
type Lane struct {
	Idx   int    `json:"idx"`
	Name  string `json:"name"`
	Tog   uint16 `json:"tog"` // toggles over the last 65536 bus clocks (saturating)
	Level uint8  `json:"level"`
	Ever1 bool   `json:"ever1"`
	Ever0 bool   `json:"ever0"`
}

// Census is the lane/bus census.
// TRLC-LINKS: REQ-SDS-144
type Census struct {
	At            string            `json:"at"`
	ClkStat       RegVal            `json:"clk_stat"`
	MiscRd        RegVal            `json:"misc_rd"`
	Snoop         RegVal            `json:"snoop_sel"`
	SnoopData     uint16            `json:"snoop_data"`
	StatusA       RegVal            `json:"status_a"`
	DiagCtrl      RegVal            `json:"diag_ctrl"`
	BusRdLo       uint16            `json:"bus_rd_lo"`
	BusRdHi       uint16            `json:"bus_rd_hi"`
	BusLevels     map[string]uint8  `json:"bus_levels"`
	AdcHold       uint16            `json:"adc_hold"`
	Lanes         []Lane            `json:"lanes"`
	LanesToggling int               `json:"lanes_toggling"`
	Summary       map[string]string `json:"summary"`
}

// TRLC-LINKS: REQ-SDS-144
func reg(b bus.Bus, sel uint16) RegVal {
	v, _ := b.Read(bus.PlaneCS1, sel)
	r, _ := iface.BySel(sel)
	return decode(r, v)
}

// Census reads every toggle counter and level (all lanes, bus balls, singles,
// P6/A2/B1/K2), the bus readback, the clock status and the snoop registers.
// It leaves LANE_IDX where it found it.
// TRLC-LINKS: REQ-SDS-144
func (d *Diag) Census() (*Census, error) {
	c := &Census{BusLevels: map[string]uint8{}, Summary: map[string]string{}}
	err := d.run.Exec(func(b bus.Bus) error {
		c.ClkStat = reg(b, iface.SelClkStat)
		c.MiscRd = reg(b, iface.SelMiscRd)
		c.Snoop = reg(b, iface.SelSnoopSel)
		c.SnoopData, _ = b.Read(bus.PlaneCS1, iface.SelSnoopData)
		c.StatusA = reg(b, iface.SelStatusA)
		c.DiagCtrl = reg(b, iface.SelDiagCtrl)
		saved, err := b.Read(bus.PlaneCS1, iface.SelDiagIdx)
		if err != nil {
			return err
		}
		c.BusRdLo, _ = winRead(b, iface.DiagBusRdLo)
		c.BusRdHi, _ = winRead(b, iface.DiagBusRdHi)
		c.AdcHold, _ = winRead(b, iface.DiagAdcHold)
		for i, ball := range BusBalls {
			c.BusLevels[ball] = uint8(busBit(c.BusRdLo, c.BusRdHi, i))
		}
		for _, idx := range laneIndices() {
			if err := winWrite(b, iface.DiagLaneIdx, uint16(idx)); err != nil {
				return err
			}
			// LANE_TOG is one windowed counter on the selected signal: a reading
			// is valid two 65536-cycle windows (2.6 ms at 50 MHz) after the
			// index changes (fpga/common/lane_in.v). Hardware run 2026-09-05
			// without this wait reported the previous lane's count on every lane.
			d.sleep(laneTogSettle)
			tog, _ := winRead(b, iface.DiagLaneTog)
			lvl, _ := winRead(b, iface.DiagLaneLvl)
			l := Lane{Idx: idx, Name: LaneName(idx), Tog: tog,
				Level: uint8(lvl & iface.DiagLaneLvlLevelMask),
				Ever1: lvl&iface.DiagLaneLvlEver1Mask != 0,
				Ever0: lvl&iface.DiagLaneLvlEver0Mask != 0}
			if tog > 0 {
				c.LanesToggling++
			}
			c.Lanes = append(c.Lanes, l)
		}
		return b.Write(bus.PlaneCS1, iface.SelDiagIdx, saved)
	}, d.timeout)
	if err != nil {
		return nil, err
	}
	c.At = time.Now().Format(time.RFC3339)
	adcTog, busTog := 0, 0
	for _, l := range c.Lanes {
		if l.Idx < 80 && l.Tog > 0 {
			adcTog++
		}
		if l.Idx >= 0x50 && l.Idx < 0x50+len(BusBalls) && l.Tog > 0 {
			busTog++
		}
	}
	c.Summary["adc_lanes_toggling"] = fmt.Sprintf("%d/80", adcTog)
	c.Summary["bus_balls_toggling"] = fmt.Sprintf("%d/%d", busTog, len(BusBalls))
	c.Summary["pll"] = fmt.Sprintf("A=%d B=%d", c.ClkStat.Fields["PLLA_LOCK"], c.ClkStat.Fields["PLLB_LOCK"])
	d.mu.Lock()
	d.lastCens, d.censAt = c, time.Now()
	d.mu.Unlock()
	return c, nil
}

// TRLC-LINKS: REQ-SDS-144, REQ-SDS-145
func busBit(lo, hi uint16, i int) uint16 {
	if i < 16 {
		return (lo >> uint(i)) & 1
	}
	return (hi >> uint(i-16)) & 1
}

// TRLC-LINKS: REQ-SDS-144, REQ-SDS-145
func setBusBit(lo, hi uint16, i int, v bool) (uint16, uint16) {
	if i < 16 {
		if v {
			return lo | 1<<uint(i), hi
		}
		return lo &^ (1 << uint(i)), hi
	}
	if v {
		return lo, hi | 1<<uint(i-16)
	}
	return lo, hi &^ (1 << uint(i-16))
}

// ---- snapshot RAM ----

// Snapshot arms the snapshot RAM on the selected slice and clock, waits for it
// to complete and pops every word. mode is DIAG SNAP_MODE.SLICE (0..7), clk is
// DIAG_CTRL.SNAP_CLK (0..3).
// TRLC-LINKS: REQ-SDS-144
func (d *Diag) Snapshot(mode, clk int) ([]uint16, error) {
	if mode < 0 || mode > 7 || clk < 0 || clk > 3 {
		return nil, fmt.Errorf("diag: snapshot mode %d / clk %d out of range", mode, clk)
	}
	var words []uint16
	err := d.run.Exec(func(b bus.Bus) error {
		if err := winWrite(b, iface.DiagSnapMode, uint16(mode)); err != nil {
			return err
		}
		ctl, err := b.Read(bus.PlaneCS1, iface.SelDiagCtrl)
		if err != nil {
			return err
		}
		base := ctl &^ (iface.DiagCtrlSnapClkMask | iface.DiagCtrlSnapArmMask)
		base |= uint16(clk) << iface.DiagCtrlSnapClkShift
		if err := b.Write(bus.PlaneCS1, iface.SelDiagCtrl, base|iface.DiagCtrlSnapArmMask); err != nil {
			return err
		}
		if err := b.Write(bus.PlaneCS1, iface.SelDiagCtrl, base); err != nil { // arm is a pulse
			return err
		}
		var rem uint16
		for i := 0; i < 200; i++ {
			rem, _ = b.Read(bus.PlaneCS1, iface.SelSnapRemain)
			if rem&iface.SnapRemainReadyMask != 0 {
				break
			}
			d.sleep(time.Millisecond)
		}
		if rem&iface.SnapRemainReadyMask == 0 {
			return fmt.Errorf("diag: snapshot never became ready (SNAP_REMAIN %#04x)", rem)
		}
		n := int(rem & iface.SnapRemainRemainMask)
		words = make([]uint16, n)
		b.PopWords(iface.SelSnapPop, words, n)
		return nil
	}, d.timeout)
	if err != nil {
		return nil, err
	}
	return words, nil
}

// ---- bus drive ----

// BusState is the drive state and readback of the 27-ball bus and the singles.
// TRLC-LINKS: REQ-SDS-145
type BusState struct {
	Master   bool             `json:"master"` // DIAG_CTRL.BUS_DRV_EN
	OeLo     uint16           `json:"oe_lo"`
	OeHi     uint16           `json:"oe_hi"`
	DrvLo    uint16           `json:"drv_lo"`
	DrvHi    uint16           `json:"drv_hi"`
	RdLo     uint16           `json:"rd_lo"`
	RdHi     uint16           `json:"rd_hi"`
	AdcHold  uint16           `json:"adc_hold"`
	Single   uint16           `json:"single_ctrl"`
	D2       uint8            `json:"d2"`
	P6       uint8            `json:"p6"`
	Balls    map[string]Ball  `json:"balls"`
	Singles  map[string]uint8 `json:"singles"` // MISC_RD levels
	DiagCtrl uint16           `json:"diag_ctrl"`
}

// Ball is one bus ball's state.
// TRLC-LINKS: REQ-SDS-145
type Ball struct {
	OE    bool  `json:"oe"`
	Drive uint8 `json:"drive"`
	Read  uint8 `json:"read"`
}

// TRLC-LINKS: REQ-SDS-145
func readBus(b bus.Bus) (BusState, error) {
	var s BusState
	var err error
	if s.DiagCtrl, err = b.Read(bus.PlaneCS1, iface.SelDiagCtrl); err != nil {
		return s, err
	}
	s.Master = s.DiagCtrl&iface.DiagCtrlBusDrvEnMask != 0
	s.D2 = uint8(s.DiagCtrl & iface.DiagCtrlD2Mask)
	s.OeLo, _ = winRead(b, iface.DiagBusOeLo)
	s.OeHi, _ = winRead(b, iface.DiagBusOeHi)
	s.DrvLo, _ = winRead(b, iface.DiagBusDrvLo)
	s.DrvHi, _ = winRead(b, iface.DiagBusDrvHi)
	s.RdLo, _ = winRead(b, iface.DiagBusRdLo)
	s.RdHi, _ = winRead(b, iface.DiagBusRdHi)
	s.AdcHold, _ = winRead(b, iface.DiagAdcHold)
	s.Single, _ = winRead(b, iface.DiagSingleCtrl)
	misc, _ := b.Read(bus.PlaneCS1, iface.SelMiscRd)
	s.P6 = uint8(misc & iface.MiscRdP6Mask)
	s.Balls = map[string]Ball{}
	for i, ball := range BusBalls {
		s.Balls[ball] = Ball{OE: busBit(s.OeLo, s.OeHi, i) == 1, Drive: uint8(busBit(s.DrvLo, s.DrvHi, i)), Read: uint8(busBit(s.RdLo, s.RdHi, i))}
	}
	s.Singles = map[string]uint8{}
	for i, ball := range SingleBalls {
		s.Singles[ball] = uint8((misc >> uint(iface.MiscRdF3Shift+i)) & 1)
	}
	return s, nil
}

// BusRead returns the bus drive state and readback.
// TRLC-LINKS: REQ-SDS-145
func (d *Diag) BusRead() (BusState, error) {
	var s BusState
	err := d.run.Exec(func(b bus.Bus) error {
		var e error
		s, e = readBus(b)
		return e
	}, d.timeout)
	return s, err
}

// BusDrive sets one bus ball: oe=false tri-states it; oe=true drives level.
// The master enable (DIAG_CTRL.BUS_DRV_EN) is left as it is — see BusMaster.
// TRLC-LINKS: REQ-SDS-145
func (d *Diag) BusDrive(ball string, oe bool, level uint8) error {
	i, ok := BusBallIndex(ball)
	if !ok {
		return fmt.Errorf("diag: %q is not a bus ball", ball)
	}
	return d.run.Exec(func(b bus.Bus) error { return driveBall(b, i, oe, level == 1) }, d.timeout)
}

// TRLC-LINKS: REQ-SDS-145
func driveBall(b bus.Bus, i int, oe, level bool) error {
	oeLo, err := winRead(b, iface.DiagBusOeLo)
	if err != nil {
		return err
	}
	oeHi, _ := winRead(b, iface.DiagBusOeHi)
	drvLo, _ := winRead(b, iface.DiagBusDrvLo)
	drvHi, _ := winRead(b, iface.DiagBusDrvHi)
	drvLo, drvHi = setBusBit(drvLo, drvHi, i, level)
	oeLo, oeHi = setBusBit(oeLo, oeHi, i, oe)
	// level first, then enable — never enable a stale level
	if err := winWrite(b, iface.DiagBusDrvLo, drvLo); err != nil {
		return err
	}
	if err := winWrite(b, iface.DiagBusDrvHi, drvHi); err != nil {
		return err
	}
	if err := winWrite(b, iface.DiagBusOeLo, oeLo); err != nil {
		return err
	}
	return winWrite(b, iface.DiagBusOeHi, oeHi)
}

// BusMaster sets DIAG_CTRL.BUS_DRV_EN, the master enable of every per-ball OE.
// TRLC-LINKS: REQ-SDS-145
func (d *Diag) BusMaster(on bool) error {
	return d.run.Exec(func(b bus.Bus) error { return setCtrlBits(b, iface.DiagCtrlBusDrvEnMask, on) }, d.timeout)
}

// BusReleaseAll tri-states every bus ball and clears the master enable.
// TRLC-LINKS: REQ-SDS-145
func (d *Diag) BusReleaseAll() error {
	return d.run.Exec(func(b bus.Bus) error {
		if err := setCtrlBits(b, iface.DiagCtrlBusDrvEnMask, false); err != nil {
			return err
		}
		if err := winWrite(b, iface.DiagBusOeLo, 0); err != nil {
			return err
		}
		return winWrite(b, iface.DiagBusOeHi, 0)
	}, d.timeout)
}

// SetCtrl sets/clears DIAG_CTRL bits by mask (D2, K2_EN, A11, F1, G1, G2, K1 …).
// TRLC-LINKS: REQ-SDS-145
func (d *Diag) SetCtrl(mask uint16, on bool) error {
	return d.run.Exec(func(b bus.Bus) error { return setCtrlBits(b, mask, on) }, d.timeout)
}

// TRLC-LINKS: REQ-SDS-145
func setCtrlBits(b bus.Bus, mask uint16, on bool) error {
	v, err := b.Read(bus.PlaneCS1, iface.SelDiagCtrl)
	if err != nil {
		return err
	}
	v &^= iface.DiagCtrlSnapArmMask | iface.DiagCtrlLaneGateRstMask // never re-pulse the strobes
	if on {
		v |= mask
	} else {
		v &^= mask
	}
	return b.Write(bus.PlaneCS1, iface.SelDiagCtrl, v)
}

// ---- register save/restore around experiments ----

// savedRegs are the RW fabric registers an experiment may disturb (v3: the
// test source and the drain window included, so a test-source capture never
// leaves the engine draining a ramp or a partial window).
var savedRegs = []uint16{
	iface.SelRun, iface.SelDecimLo, iface.SelDecimHi, iface.SelPretrigLo, iface.SelPretrigHi,
	iface.SelPosttrigLo, iface.SelPosttrigHi, iface.SelAcqCtrl, iface.SelTrigLevel, iface.SelDiagCtrl, iface.SelDiagIdx,
	iface.SelIlCtrl, iface.SelDrainStart, iface.SelDrainLen,
}

// TRLC-LINKS: REQ-SDS-145, REQ-SDS-146, REQ-SDS-147, REQ-SDS-148
type regSnapshot struct {
	regs map[uint16]uint16
	win  map[uint16]uint16
}

// TRLC-LINKS: REQ-SDS-145, REQ-SDS-146, REQ-SDS-147, REQ-SDS-148
func saveRegs(b bus.Bus, win ...uint16) (regSnapshot, error) {
	s := regSnapshot{regs: map[uint16]uint16{}, win: map[uint16]uint16{}}
	for _, sel := range savedRegs {
		v, err := b.Read(bus.PlaneCS1, sel)
		if err != nil {
			return s, err
		}
		s.regs[sel] = v
	}
	for _, idx := range win {
		v, err := winRead(b, idx)
		if err != nil {
			return s, err
		}
		s.win[idx] = v
	}
	return s, nil
}

// restore puts every saved word back (DIAG_IDX last, strobes masked) and
// re-arms the capture when RUN.RUN was set, so the engine's frame in flight
// resumes on its own program.
// TRLC-LINKS: REQ-SDS-145, REQ-SDS-146, REQ-SDS-147, REQ-SDS-148
func (s regSnapshot) restore(b bus.Bus) error {
	var first error
	keep := func(err error) {
		if err != nil && first == nil {
			first = err
		}
	}
	keep(b.Write(bus.PlaneCS1, iface.SelOpcode, iface.OpReset))
	for _, sel := range savedRegs {
		if sel == iface.SelDiagIdx {
			continue
		}
		v := s.regs[sel]
		if sel == iface.SelDiagCtrl {
			v &^= iface.DiagCtrlSnapArmMask | iface.DiagCtrlLaneGateRstMask
		}
		keep(b.Write(bus.PlaneCS1, sel, v))
	}
	idxs := make([]int, 0, len(s.win))
	for idx := range s.win {
		idxs = append(idxs, int(idx))
	}
	sort.Ints(idxs)
	for _, idx := range idxs {
		keep(winWrite(b, uint16(idx), s.win[uint16(idx)]))
	}
	keep(b.Write(bus.PlaneCS1, iface.SelDiagIdx, s.regs[iface.SelDiagIdx]))
	if s.regs[iface.SelRun]&iface.RunRunMask != 0 {
		keep(b.Write(bus.PlaneCS1, iface.SelOpcode, iface.OpGo))
	}
	return first
}

// ---- E1: the vendor CS1 word sequence ----

// VendorStep is one raw write and what the fabric showed right after it.
// TRLC-LINKS: REQ-SDS-145
type VendorStep struct {
	Sel     uint16 `json:"sel"`
	Val     uint16 `json:"val"`
	Snoop   RegVal `json:"snoop_sel"`
	Data    uint16 `json:"snoop_data"`
	MiscRd  RegVal `json:"misc_rd"`
	BusRdLo uint16 `json:"bus_rd_lo"`
	BusRdHi uint16 `json:"bus_rd_hi"`
	StatusA uint16 `json:"status_a"`
}

// VendorResult is the E1 observation.
// TRLC-LINKS: REQ-SDS-145
type VendorResult struct {
	Before   VendorStep   `json:"before"`
	Steps    []VendorStep `json:"steps"`
	After    VendorStep   `json:"after"`
	Restored bool         `json:"restored"`
	Changed  []string     `json:"changed"` // what moved between before and after (P6, bus readback bits)
}

// vendorWords is the factory arm/halt sequence the app used to issue on the
// factory fabric (04-DESIGN §3 case B; app engine on main): reset-head ×2,
// write-pointer pulse, go, then halt. On our map they alias onto OPCODE
// (ignored values) and FILL (read-only) — the MAX V snoops them regardless.
var vendorWords = []struct{ sel, val uint16 }{
	{0x21, 0x00c0}, {0x21, 0x00c0}, {0x57, 0x0001}, {0x57, 0x0000}, {0x21, 0x00c3},
	{0x21, 0x00c8},
}

// TRLC-LINKS: REQ-SDS-145
func observe(b bus.Bus, sel, val uint16) VendorStep {
	st := VendorStep{Sel: sel, Val: val}
	st.Snoop = reg(b, iface.SelSnoopSel)
	st.Data, _ = b.Read(bus.PlaneCS1, iface.SelSnoopData)
	st.MiscRd = reg(b, iface.SelMiscRd)
	st.BusRdLo, _ = winRead(b, iface.DiagBusRdLo)
	st.BusRdHi, _ = winRead(b, iface.DiagBusRdHi)
	st.StatusA, _ = b.Read(bus.PlaneCS1, iface.SelStatusA)
	return st
}

// VendorSequence issues the vendor words as raw selector writes with a dwell
// after the GO word, observing P6 / the bus readback / the snoop registers
// after every write, then restores our registers.
// TRLC-LINKS: REQ-SDS-145
func (d *Diag) VendorSequence(dwell time.Duration) (*VendorResult, error) {
	res := &VendorResult{}
	err := d.run.Exec(func(b bus.Bus) error {
		snap, err := saveRegs(b)
		if err != nil {
			return err
		}
		res.Before = observe(b, 0, 0)
		for i, w := range vendorWords {
			if err := b.RawWrite(w.sel, w.val); err != nil {
				return err
			}
			if w.val == 0x00c3 && dwell > 0 {
				d.sleep(dwell)
			}
			res.Steps = append(res.Steps, observe(b, w.sel, w.val))
			_ = i
		}
		res.After = observe(b, 0, 0)
		res.Restored = snap.restore(b) == nil
		return nil
	}, d.timeout+dwell)
	if err != nil {
		return nil, err
	}
	if res.Before.MiscRd.Fields["P6"] != res.After.MiscRd.Fields["P6"] {
		res.Changed = append(res.Changed, "P6")
	}
	for i, ball := range BusBalls {
		if busBit(res.Before.BusRdLo, res.Before.BusRdHi, i) != busBit(res.After.BusRdLo, res.After.BusRdHi, i) {
			res.Changed = append(res.Changed, ball)
		}
	}
	return res, nil
}

// ---- E2: bus ownership ----

// E2Options tunes the ownership experiment.
// TRLC-LINKS: REQ-SDS-145
type E2Options struct {
	Blocks         int  `json:"blocks"`           // alternating blocks per condition (default 3, min 3)
	Repeats        int  `json:"repeats"`          // drive/read repeats per ball per block (default 4)
	IncludeADCHold bool `json:"include_adc_hold"` // release L4/T2/T7 from the ADC recipe for the run
}

// BallScore is one ball's follow rates under one D2 condition.
// TRLC-LINKS: REQ-SDS-145
type BallScore struct {
	Ball      string    `json:"ball"`
	Follow0   float64   `json:"follow0"`   // drove 0, read 0
	Follow1   float64   `json:"follow1"`   // drove 1, read 1
	RestHigh  float64   `json:"rest_high"` // released, read 1
	PerBlock0 []float64 `json:"per_block0"`
	PerBlock1 []float64 `json:"per_block1"`
	Spread    float64   `json:"spread"` // max-min of the per-block follow rate (both levels) — within-condition spread
	Held      bool      `json:"held,omitempty"`
}

// E2Condition is one D2 state.
// TRLC-LINKS: REQ-SDS-145
type E2Condition struct {
	D2     uint8       `json:"d2"`
	Scores []BallScore `json:"scores"`
	Blocks []int       `json:"block_order"` // global block numbers this condition ran in
}

// E2Result is the ownership experiment output.
// TRLC-LINKS: REQ-SDS-145
type E2Result struct {
	Options    E2Options     `json:"options"`
	Conditions []E2Condition `json:"conditions"`
	Order      []uint8       `json:"order"` // D2 per global block (alternating)
	Restored   bool          `json:"restored"`
	Summary    []string      `json:"summary"`
}

// TRLC-LINKS: REQ-SDS-145
type tally struct {
	f0, f1, rest []int // per block: counts
	n            []int
}

// E2 runs the bus-ownership experiment: for each alternating D2 block and
// each bus ball, drive the ball alone at 0 and at 1 (master enable on) and
// read the registered pad back; release it and read the rest level. Follow
// rates are reported per ball per condition with the per-block spread.
// TRLC-LINKS: REQ-SDS-145
func (d *Diag) E2(o E2Options) (*E2Result, error) {
	if o.Blocks < 3 {
		o.Blocks = 3
	}
	if o.Repeats <= 0 {
		o.Repeats = 4
	}
	res := &E2Result{Options: o}
	tal := map[uint8]map[string]*tally{0: {}, 1: {}}
	for _, ball := range BusBalls {
		for _, c := range []uint8{0, 1} {
			tal[c][ball] = &tally{f0: make([]int, o.Blocks), f1: make([]int, o.Blocks), rest: make([]int, o.Blocks), n: make([]int, o.Blocks)}
		}
	}
	blockOf := map[uint8][]int{}
	err := d.run.Exec(func(b bus.Bus) error {
		snap, err := saveRegs(b, iface.DiagBusOeLo, iface.DiagBusOeHi, iface.DiagBusDrvLo, iface.DiagBusDrvHi, iface.DiagAdcHold)
		if err != nil {
			return err
		}
		defer func() { res.Restored = snap.restore(b) == nil }()
		hold := snap.win[iface.DiagAdcHold]
		if o.IncludeADCHold {
			if err := winWrite(b, iface.DiagAdcHold, 0); err != nil {
				return err
			}
			hold = 0
		}
		// start released, K2 running, master on
		if err := winWrite(b, iface.DiagBusOeLo, 0); err != nil {
			return err
		}
		if err := winWrite(b, iface.DiagBusOeHi, 0); err != nil {
			return err
		}
		if err := setCtrlBits(b, iface.DiagCtrlK2EnMask|iface.DiagCtrlBusDrvEnMask, true); err != nil {
			return err
		}
		global := 0
		for blk := 0; blk < o.Blocks; blk++ {
			for _, c := range []uint8{0, 1} { // alternate D2 0/1 every block
				if err := setCtrlBits(b, iface.DiagCtrlD2Mask, c == 1); err != nil {
					return err
				}
				res.Order = append(res.Order, c)
				blockOf[c] = append(blockOf[c], global)
				global++
				for i, ball := range BusBalls {
					t := tal[c][ball]
					if m, isHold := adcHoldBalls[ball]; isHold && hold&m != 0 {
						continue // held static by the ADC recipe: not driven in this run
					}
					for r := 0; r < o.Repeats; r++ {
						for _, lvl := range []bool{false, true} {
							if err := driveBall(b, i, true, lvl); err != nil {
								return err
							}
							lo, _ := winRead(b, iface.DiagBusRdLo)
							hi, _ := winRead(b, iface.DiagBusRdHi)
							got := busBit(lo, hi, i) == 1
							if got == lvl {
								if lvl {
									t.f1[blk]++
								} else {
									t.f0[blk]++
								}
							}
						}
						if err := driveBall(b, i, false, false); err != nil {
							return err
						}
						// let the released ball settle on its pull-up before
						// reading the rest level (2026-09-05: read-immediately
						// scored rest_high 0 while the census read all ones)
						d.sleep(busSettle)
						lo, _ := winRead(b, iface.DiagBusRdLo)
						hi, _ := winRead(b, iface.DiagBusRdHi)
						if busBit(lo, hi, i) == 1 {
							t.rest[blk]++
						}
						t.n[blk]++
					}
				}
			}
		}
		return nil
	}, d.timeout+time.Duration(o.Blocks*o.Repeats)*100*time.Millisecond)
	if err != nil {
		return nil, err
	}
	for _, c := range []uint8{0, 1} {
		cond := E2Condition{D2: c, Blocks: blockOf[c]}
		for _, ball := range BusBalls {
			t := tal[c][ball]
			sc := BallScore{Ball: ball}
			tot, s0, s1, sr := 0, 0, 0, 0
			minR, maxR := 2.0, -1.0
			for blk := 0; blk < o.Blocks; blk++ {
				n := t.n[blk]
				tot += n
				s0 += t.f0[blk]
				s1 += t.f1[blk]
				sr += t.rest[blk]
				if n == 0 {
					sc.PerBlock0 = append(sc.PerBlock0, 0)
					sc.PerBlock1 = append(sc.PerBlock1, 0)
					continue
				}
				r0, r1 := float64(t.f0[blk])/float64(n), float64(t.f1[blk])/float64(n)
				sc.PerBlock0 = append(sc.PerBlock0, r0)
				sc.PerBlock1 = append(sc.PerBlock1, r1)
				r := (r0 + r1) / 2
				if r < minR {
					minR = r
				}
				if r > maxR {
					maxR = r
				}
			}
			if tot == 0 {
				sc.Held = true
			} else {
				sc.Follow0 = float64(s0) / float64(tot)
				sc.Follow1 = float64(s1) / float64(tot)
				sc.RestHigh = float64(sr) / float64(tot)
				sc.Spread = maxR - minR
			}
			cond.Scores = append(cond.Scores, sc)
		}
		res.Conditions = append(res.Conditions, cond)
		follow := 0
		for _, sc := range cond.Scores {
			if !sc.Held && sc.Follow0 >= 0.99 && sc.Follow1 >= 0.99 {
				follow++
			}
		}
		res.Summary = append(res.Summary, fmt.Sprintf("D2=%d: %d/%d balls follow (0 and 1) across %d blocks", c, follow, len(BusBalls), o.Blocks))
	}
	return res, nil
}

// ---- diag capture (acceptance: ramp/drain check) ----

// CaptureOptions tunes a diagnostic capture.
// TRLC-LINKS: REQ-SDS-148
type CaptureOptions struct {
	Words   int    `json:"words"`    // record words (default and maximum iface.PretrigMax = 20478: the fabric finalizes pre+post <= PRETRIG_MAX)
	Decim   uint32 `json:"decim"`    // default 1
	EncRate int    `json:"enc_rate"` // ACQ_CTRL.ENC_RATE (default 3 = 200 MHz)
	Samples bool   `json:"samples"`  // include the samples in the JSON result
	Tsrc    uint16 `json:"tsrc"`     // IL_CTRL.TSRC: 0 ADC (default), 1 RAMP, 2 COLTAG, 3 GLITCH — the drained words are checked against iface.TsrcCheck (rung R2b)
	Chmode  uint16 `json:"chmode"`   // RUN.CHMODE 0 dual (default), 1 CH1, 2 CH2
	Start   uint16 `json:"start"`    // drain window start (DRAIN_START); with Len 0 and Start 0 the engine's plain BURST drain is used
	Len     uint16 `json:"len"`      // drain window length (DRAIN_LEN, 0 = to the record end)
}

// CaptureResult scores a drained record.
// TRLC-LINKS: REQ-SDS-148
type CaptureResult struct {
	Words       int      `json:"words"`           // words drained
	Requested   int      `json:"requested_words"` // words programmed (pre+post); != Words is a fabric shortfall
	Remain      uint16   `json:"burst_remain"`
	StatusA     uint16   `json:"status_a"`
	Fill        uint16   `json:"fill"`
	RampBreaks1 int      `json:"ramp_breaks_ch1"` // steps where c[i+1]-c[i] != +1 (mod 256)
	RampBreaks2 int      `json:"ramp_breaks_ch2"`
	Min1        uint8    `json:"min_ch1"`
	Max1        uint8    `json:"max_ch1"`
	Min2        uint8    `json:"min_ch2"`
	Max2        uint8    `json:"max_ch2"`
	Mean1       float64  `json:"mean_ch1"`
	Mean2       float64  `json:"mean_ch2"`
	NonZero     int      `json:"nonzero_words"`
	Distinct1   int      `json:"distinct_ch1"`
	Distinct2   int      `json:"distinct_ch2"`
	First       []uint16 `json:"first"`
	C1          []uint8  `json:"c1,omitempty"`
	C2          []uint8  `json:"c2,omitempty"`
	Restored    bool     `json:"restored"`
	// v3: the test source / window / monitors of this drain.
	Tsrc      uint16        `json:"tsrc"`
	Chmode    uint16        `json:"chmode"`
	Window    bus.Window    `json:"window"`
	Exact     bus.Exactness `json:"exact"` // pointer-delta + pattern verdict (the pattern part only with Tsrc != 0)
	NsPerWord float64       `json:"ns_per_word"`
	DrainStat bus.DrainStat `json:"drain_stat"` // after the drain
}

// Capture programs a one-shot auto capture (RESET, RUN auto, DECIM, PRE/POST,
// ACQ_CTRL, GO), waits for DONE/VALID, halts, drains BURST and scores the
// record: ramp breaks per channel (the bench Au ramp / an in-fabric ramp),
// non-zero payload count, per-channel range. Restores the engine's program.
// TRLC-LINKS: REQ-SDS-148
func (d *Diag) Capture(o CaptureOptions) (*CaptureResult, error) {
	// The fabric clamps POSTTRIG to PRETRIG_MAX - PRETRIG (default.v), so a
	// record is at most PRETRIG_MAX words; asking for REC_DEPTH would finalize
	// two words fewer than requested and read as a drain shortfall on the bench.
	if o.Words <= 0 || o.Words > iface.PretrigMax {
		o.Words = iface.PretrigMax
	}
	if o.Decim == 0 {
		o.Decim = 1
	}
	if o.EncRate < 0 || o.EncRate > 3 {
		o.EncRate = 3
	}
	if o.Tsrc > iface.IlCtrlTsrcGlitch || o.Chmode > iface.RunChmodeCh2 {
		return nil, fmt.Errorf("diag: tsrc %d / chmode %d out of range", o.Tsrc, o.Chmode)
	}
	res := &CaptureResult{Words: o.Words, Requested: o.Words, Tsrc: o.Tsrc, Chmode: o.Chmode, Window: bus.Window{Start: o.Start, Len: o.Len}}
	err := d.run.Exec(func(b bus.Bus) error {
		snap, err := saveRegs(b)
		if err != nil {
			return err
		}
		defer func() { res.Restored = snap.restore(b) == nil }()
		pre, post := uint32(o.Words/2), uint32(o.Words-o.Words/2)
		w := func(sel, v uint16) {
			if err == nil {
				err = b.Write(bus.PlaneCS1, sel, v)
			}
		}
		w(iface.SelOpcode, iface.OpReset)
		w(iface.SelIlCtrl, o.Tsrc<<iface.IlCtrlTsrcShift)
		w(iface.SelRun, iface.RunRunMask|o.Chmode<<iface.RunChmodeShift) // auto
		w(iface.SelDecimLo, uint16(o.Decim))
		w(iface.SelDecimHi, uint16(o.Decim>>16))
		w(iface.SelPretrigLo, uint16(pre))
		w(iface.SelPretrigHi, uint16(pre>>16))
		w(iface.SelPosttrigLo, uint16(post))
		w(iface.SelPosttrigHi, uint16(post>>16))
		w(iface.SelDrainStart, o.Start)
		w(iface.SelDrainLen, o.Len)
		w(iface.SelAcqCtrl, iface.AcqCtrlEncEnMask|uint16(o.EncRate)<<iface.AcqCtrlEncRateShift|iface.AcqCtrlPairEnMask)
		w(iface.SelTrigLevel, 128)
		w(iface.SelOpcode, iface.OpGo)
		if err != nil {
			return err
		}
		for i := 0; i < 500; i++ {
			res.StatusA, _ = b.Read(bus.PlaneCS1, iface.SelStatusA)
			if res.StatusA&(iface.StatusADoneMask|iface.StatusAValidMask) != 0 {
				break
			}
			d.sleep(time.Millisecond)
		}
		w(iface.SelOpcode, iface.OpHalt)
		res.Fill, _ = b.Read(bus.PlaneCS1, iface.SelFill)
		res.Remain, _ = b.Read(bus.PlaneCS1, iface.SelBurstRemain)
		n := int(res.Remain & iface.BurstRemainRemainMask)
		if res.Remain&iface.BurstRemainReadyMask == 0 {
			return fmt.Errorf("diag: no record ready after the capture (STATUS_A %#04x, BURST_REMAIN %#04x)", res.StatusA, res.Remain)
		}
		if n > o.Words {
			n = o.Words
		}
		res.C1 = make([]uint8, n)
		res.C2 = make([]uint8, n)
		var dr bus.DrainResult
		if o.Start == 0 && o.Len == 0 {
			// The engine's own path: BURST_REMAIN then BurstInto (EDMA), with the
			// monitors read around it.
			dr.Before, _ = bus.ReadDrainStat(b)
			t0 := time.Now()
			b.BurstInto(res.C1, res.C2, n)
			dr.Elapsed = time.Since(t0)
			dr.N = n
			dr.After, _ = bus.ReadDrainStat(b)
		} else {
			// A window: DRAIN_START/LEN + REWIND on the frozen record.
			scratch := make([]uint16, n)
			if dr, err = bus.DrainWindowInto(b, res.Window, res.C1, res.C2, scratch); err != nil {
				return err
			}
			n = dr.N
			res.C1, res.C2 = res.C1[:n], res.C2[:n]
		}
		res.Words = n
		res.NsPerWord = dr.NsPerWord()
		res.DrainStat = dr.After
		words := make([]uint16, n)
		for i := range words {
			words[i] = iface.Word(res.C1[i], res.C2[i])
		}
		res.Exact = bus.CheckExact(dr, words, o.Tsrc, nil)
		return nil
	}, d.timeout)
	if err != nil {
		return nil, err
	}
	score(res)
	if !o.Samples {
		res.C1, res.C2 = nil, nil
	}
	return res, nil
}

// TRLC-LINKS: REQ-SDS-148
func score(r *CaptureResult) {
	n := len(r.C1)
	if n == 0 {
		return
	}
	r.Min1, r.Max1, r.Min2, r.Max2 = 255, 0, 255, 0
	var s1, s2 int
	var seen1, seen2 [256]bool
	for i := 0; i < n; i++ {
		a, b := r.C1[i], r.C2[i]
		s1 += int(a)
		s2 += int(b)
		seen1[a], seen2[b] = true, true
		if a < r.Min1 {
			r.Min1 = a
		}
		if a > r.Max1 {
			r.Max1 = a
		}
		if b < r.Min2 {
			r.Min2 = b
		}
		if b > r.Max2 {
			r.Max2 = b
		}
		if a != 0 || b != 0 {
			r.NonZero++
		}
		if i > 0 {
			if uint8(a-r.C1[i-1]) != 1 {
				r.RampBreaks1++
			}
			if uint8(b-r.C2[i-1]) != 1 {
				r.RampBreaks2++
			}
		}
		if i < 32 {
			r.First = append(r.First, uint16(a)<<8|uint16(b))
		}
	}
	r.Mean1 = float64(s1) / float64(n)
	r.Mean2 = float64(s2) / float64(n)
	for v := 0; v < 256; v++ {
		if seen1[v] {
			r.Distinct1++
		}
		if seen2[v] {
			r.Distinct2++
		}
	}
}
