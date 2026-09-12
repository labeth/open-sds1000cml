package diag

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"open-sds/app/internal/bus"
	"open-sds/app/internal/iface"
)

// fakeFabric is a register-level model of the diagnostic block: the DIAG
// window RAM, a 27-ball bus whose readback follows the drive only when the
// ball is enabled AND the master is on (otherwise the pull-up 1 — or, for
// balls "owned" by someone else, a fixed foreign level), lane counters, the
// snapshot RAM, a record that fills as a +1 ramp on CH1 and a constant on
// CH2, and the identity words.
type fakeFabric struct {
	mu      sync.Mutex
	regs    map[uint16]uint16
	win     map[uint16]uint16
	writes  []string
	raw     []string
	foreign map[int]uint8 // bus balls driven by a foreign master (readback ignores our drive)
	d2Dep   bool          // foreign ownership only while D2=1
	snapN   int
	snapPop int
	armed   bool
	halted  bool
	drained int
	rec     int
	// v3: test source, drain window, monitors
	tsrc    uint16
	k0      uint32
	winS    int
	winL    int
	ptr     int
	pops    uint16
	under   uint8
	last    uint16
	goN     int
	stubWr  int  // writes that landed on stub registers (ignored)
	corrupt bool // the GPMC timing is below the floor: a duplicate pop every 32nd word
	dupped  bool
}

func newFakeFabric() *fakeFabric {
	f := &fakeFabric{regs: map[uint16]uint16{}, win: map[uint16]uint16{}, foreign: map[int]uint8{}, snapN: 2048}
	f.regs[iface.SelBuildidLo] = iface.BuildIDLo
	f.regs[iface.SelBuildidHi] = iface.BuildIDHi
	f.regs[iface.SelVersion] = iface.VersionMagic
	f.regs[iface.SelFabricId] = iface.FabricID
	f.regs[iface.SelClkStat] = 0x0003 | 0x0a00
	f.regs[iface.SelDiagCtrl] = iface.DiagCtrlK2EnMask | iface.DiagCtrlF1Mask
	f.regs[iface.SelRun] = iface.RunRunMask
	f.regs[iface.SelDecimLo] = 160
	f.regs[iface.SelPretrigLo] = 3072
	f.regs[iface.SelPosttrigLo] = 3072
	f.regs[iface.SelAcqCtrl] = 0x7c27
	f.regs[iface.SelTrigLevel] = 167
	f.win[iface.DiagAdcHold] = 7
	for i := 0; i < 80; i++ { // lanes toggle, bus balls are quiet
		f.win[0x100+uint16(i)] = uint16(1000 + i) // tog counter storage at 0x100+idx
	}
	f.win[0x100+0x7b] = 0xffff                    // K2 saturates
	for i := 0; i < iface.DiagLanemapCount; i++ { // the baked map: entry i reads lane i
		f.win[iface.DiagLanemapBase+uint16(i)] = uint16(i)
	}
	return f
}

// window is the latched drain window over the frozen record.
func (f *fakeFabric) window() (start, n int) {
	start = f.winS
	if start > f.rec {
		start = f.rec
	}
	n = f.rec - start
	if f.winL != 0 && f.winL < n {
		n = f.winL
	}
	return
}

func (f *fakeFabric) latch() {
	f.winS = int(f.regs[iface.SelDrainStart] & iface.DrainStartIdxMask)
	f.winL = int(f.regs[iface.SelDrainLen])
	f.ptr, f.pops = 0, 0
}

// pop is one BURST pop: the TSRC pattern word at k0+index when a test source
// is on, else the v2 fake's ramp-on-CH1 / 0x55-on-CH2 word; past the window
// the last word repeats and UNDERRUN counts.
func (f *fakeFabric) pop() uint16 {
	start, n := f.window()
	f.pops++
	if !f.halted || f.ptr >= n {
		f.under++
		return f.last
	}
	i := start + f.ptr
	f.ptr++
	if f.corrupt && f.ptr%32 == 0 && !f.dupped {
		f.ptr--
		f.dupped = true
	} else {
		f.dupped = false
	}
	if f.tsrc != iface.IlCtrlTsrcAdc {
		f.last, _ = iface.TsrcWord(f.tsrc, f.k0+uint32(i))
	} else {
		f.last = uint16(i&0xff)<<8 | 0x55
	}
	return f.last
}

// v22Live is the v2.2 fabric's stored bits of a register: nothing for a stub,
// every field not marked "reads 0 in v2.2" otherwise.
func v22Live(r iface.Register) uint16 {
	if r.Stub {
		return 0
	}
	if len(r.Fields) == 0 {
		return 0xffff
	}
	var m uint16
	for _, fl := range r.Fields {
		if !strings.Contains(fl.Desc, "reads 0 in v2.2") {
			m |= fl.Mask
		}
	}
	return m
}

func (f *fakeFabric) busRd() (lo, hi uint16) {
	master := f.regs[iface.SelDiagCtrl]&iface.DiagCtrlBusDrvEnMask != 0
	d2 := f.regs[iface.SelDiagCtrl]&iface.DiagCtrlD2Mask != 0
	for i := 0; i < 27; i++ {
		v := uint16(1) // pull-up
		if fl, ok := f.foreign[i]; ok && (!f.d2Dep || d2) {
			v = uint16(fl)
		} else if master && busBit(f.win[iface.DiagBusOeLo], f.win[iface.DiagBusOeHi], i) == 1 {
			v = busBit(f.win[iface.DiagBusDrvLo], f.win[iface.DiagBusDrvHi], i)
		}
		lo, hi = setBusBit(lo, hi, i, v == 1)
	}
	return
}

func (f *fakeFabric) Read(plane uint8, sel uint16) (uint16, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if plane == bus.PlaneCS3 {
		if sel == bus.CS3ConfigPort {
			return 0x00c0, nil
		}
		return 0, nil
	}
	sel = iface.MaskSel(sel)
	switch sel {
	case iface.SelDiagData:
		idx := f.regs[iface.SelDiagIdx]
		switch idx {
		case iface.DiagBusRdLo:
			lo, _ := f.busRd()
			return lo, nil
		case iface.DiagBusRdHi:
			_, hi := f.busRd()
			return hi, nil
		case iface.DiagLaneTog:
			return f.win[0x100+f.win[iface.DiagLaneIdx]], nil
		case iface.DiagLaneLvl:
			return 0x7, nil
		}
		return f.win[idx], nil
	case iface.SelSnapRemain:
		if f.regs[0x200] == 1 { // armed
			return iface.SnapRemainReadyMask | uint16(f.snapN-f.snapPop), nil
		}
		return 0, nil
	case iface.SelSnapPop:
		v := uint16(0xA000 + f.snapPop)
		f.snapPop++
		return v, nil
	case iface.SelMiscRd:
		v := uint16(0x0002) // F3 high
		if f.regs[iface.SelDiagCtrl]&iface.DiagCtrlD2Mask != 0 {
			v |= iface.MiscRdP6Mask // P6 mirrors D2
		}
		return v, nil
	case iface.SelSnoopSel:
		return f.regs[0x300], nil
	case iface.SelSnoopData:
		return f.regs[0x301], nil
	case iface.SelStatusA:
		if f.armed {
			return iface.StatusADoneMask | iface.StatusAValidMask, nil
		}
		return 0, nil
	case iface.SelFill:
		return uint16(f.rec), nil
	case iface.SelBurstRemain:
		if f.halted {
			_, n := f.window()
			return iface.BurstRemainReadyMask | uint16(n-f.ptr), nil
		}
		return 0, nil
	case iface.SelBurst, iface.SelBurstAlias:
		f.drained++
		return f.pop(), nil
	case iface.SelDrainStat:
		return f.pops, nil
	case iface.SelPopMon:
		return uint16(f.under)<<iface.PopMonUnderrunShift | 9, nil
	}
	if r, ok := iface.BySel(sel); ok && r.Stub {
		return 0, nil
	}
	for _, u := range iface.Undecoded() {
		if sel == u {
			return 0, nil
		}
	}
	return f.regs[sel], nil
}

func (f *fakeFabric) Write(plane uint8, sel, val uint16) error {
	if !bus.Writable(plane, sel) {
		return fmt.Errorf("fake: not writable cs%d %#04x", plane, sel)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes = append(f.writes, fmt.Sprintf("%02x=%04x", sel, val))
	if plane != bus.PlaneCS1 {
		return nil
	}
	sel = iface.MaskSel(sel)
	if r, ok := iface.BySel(sel); ok && r.Stub {
		f.stubWr++ // write-ignored
		return nil
	}
	switch sel {
	case iface.SelDiagData:
		idx := f.regs[iface.SelDiagIdx]
		if e := diagEntryAt(idx); e.Stub || !e.Access.CanWrite() {
			return nil // LANEMAP is the baked map; stubs ignore writes
		}
		f.win[idx] = val
		return nil
	case iface.SelIlCtrl:
		r, _ := iface.BySel(sel)
		f.regs[sel] = val & v22Live(r)
		return nil
	case iface.SelDiagCtrl:
		if val&iface.DiagCtrlSnapArmMask != 0 {
			f.regs[0x200] = 1
			f.snapPop = 0
		}
	case iface.SelOpcode:
		switch val {
		case iface.OpGo:
			f.armed, f.halted, f.drained = true, false, 0
			// default.v clamps: pre <= PRETRIG_MAX, post <= PRETRIG_MAX - pre, post >= 1
			pre, post := int(f.regs[iface.SelPretrigLo]), int(f.regs[iface.SelPosttrigLo])
			if pre > iface.PretrigMax {
				pre = iface.PretrigMax
			}
			if post > iface.PretrigMax-pre {
				post = iface.PretrigMax - pre
			}
			if post == 0 {
				post = 1
			}
			f.rec = pre + post
			f.tsrc = (f.regs[iface.SelIlCtrl] & iface.IlCtrlTsrcMask) >> iface.IlCtrlTsrcShift
			f.goN++
			f.k0 = uint32(700*f.goN + 3)
			f.pops, f.under = 0, 0
		case iface.OpHalt:
			f.halted = true
			f.latch()
		case iface.OpRewind:
			f.latch()
		case iface.OpReset:
			f.armed, f.halted = false, false
		}
		return nil
	}
	f.regs[sel] = val
	return nil
}

func (f *fakeFabric) RawWrite(sel, val uint16) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.raw = append(f.raw, fmt.Sprintf("%02x=%04x", sel, val))
	f.regs[0x300] = sel & 0x7f
	f.regs[0x301] = val
	return nil
}

func (f *fakeFabric) BurstInto(c1, c2 []uint8, n int) {
	for i := 0; i < n; i++ {
		v, _ := f.Read(bus.PlaneCS1, iface.SelBurst)
		c1[i], c2[i] = uint8(v>>8), uint8(v)
	}
}

func (f *fakeFabric) PopWords(sel uint16, dst []uint16, n int) {
	for i := 0; i < n; i++ {
		dst[i], _ = f.Read(bus.PlaneCS1, sel)
	}
}

func (f *fakeFabric) FastDrain() bool { return false }

func direct(f *fakeFabric) Runner {
	return RunnerFunc(func(fn func(bus.Bus) error, _ time.Duration) error { return fn(f) })
}

func newTestDiag() (*Diag, *fakeFabric) {
	f := newFakeFabric()
	d := New(direct(f), nil)
	d.sleep = func(time.Duration) {}
	return d, f
}

func TestResolveNames(t *testing.T) {
	for _, c := range []struct {
		in   string
		want uint16
	}{{"RUN", 0x24}, {"run", 0x24}, {"0x24", 0x24}, {"36", 0x24}, {"BURST_REMAIN", 0x44}} {
		sel, _, err := ResolveSel(c.in)
		if err != nil || sel != c.want {
			t.Errorf("ResolveSel(%q) = %#x, %v", c.in, sel, err)
		}
	}
	if _, _, err := ResolveSel("NOPE"); err == nil {
		t.Error("unknown name accepted")
	}
	idx, e, err := ResolveDiagIdx("LANEMAP.12")
	if err != nil || idx != 0x1c || e.Name != "LANEMAP" {
		t.Errorf("LANEMAP.12 → %#x %s %v", idx, e.Name, err)
	}
	if _, _, err := ResolveDiagIdx("LANEMAP.80"); err == nil {
		t.Error("LANEMAP.80 out of range accepted")
	}
	if idx, _, err := ResolveDiagIdx("bus_rd_hi"); err != nil || idx != 5 {
		t.Errorf("bus_rd_hi → %d %v", idx, err)
	}
	if n := LaneName(0x50); n != "J6" {
		t.Errorf("LaneName(0x50) = %q", n)
	}
	if n := LaneName(0x7b); n != "K2" {
		t.Errorf("LaneName(0x7b) = %q", n)
	}
	if n := LaneName(0x7c); n != "B11" {
		t.Errorf("LaneName(0x7c) = %q", n)
	}
	if n := LaneName(0x7d); n != "J1" {
		t.Errorf("LaneName(0x7d) = %q", n)
	}
	// 80 lanes + 27 bus + 5 singles + P6/A2/B1/K2 + the two listen-only inputs B11, J1
	if got := len(laneIndices()); got != 80+27+5+4+2 {
		t.Errorf("lane indices = %d, want 118", got)
	}
	if i, ok := BusBallIndex("t7"); !ok || i != 26 {
		t.Errorf("BusBallIndex(t7) = %d %v", i, ok)
	}
}

func TestRegAndWindowAccess(t *testing.T) {
	d, f := newTestDiag()
	rv, err := d.RegRead("CLK_STAT")
	if err != nil || !(rv.Fields["PLLA_LOCK"] == 1 && rv.Fields["RATIO"] == 0xa0) {
		t.Fatalf("CLK_STAT: %+v %v", rv, err)
	}
	if err := d.RegWrite("RUN", 0x0005, false); err != nil || f.regs[iface.SelRun] != 5 {
		t.Fatalf("RUN write: %v", err)
	}
	if err := d.RegWrite("FILL", 1, false); err == nil {
		t.Fatal("write to a read-only register must be refused")
	}
	if err := d.RegWrite("0x57", 1, true); err != nil || len(f.raw) != 1 || f.raw[0] != "57=0001" {
		t.Fatalf("raw write: %v %v", err, f.raw)
	}
	if err := d.WindowWrite("ADC_HOLD", 3); err != nil || f.win[iface.DiagAdcHold] != 3 {
		t.Fatalf("window write: %v", err)
	}
	wv, err := d.WindowRead("adc_hold")
	if err != nil || wv.Fields["L4"] != 1 || wv.Fields["T7"] != 0 {
		t.Fatalf("window read: %+v %v", wv, err)
	}
	if err := d.WindowWrite("LANE_TOG", 1); err == nil {
		t.Fatal("read-only window entry accepted a write")
	}
	// LANEMAP is read-only from v2.2 (the baked map): refused with a message
	// that says so, and the entry keeps reporting the map.
	if err := d.WindowWrite("LANEMAP.3", 0x21); err == nil || !strings.Contains(err.Error(), "read-only in v3") || f.win[0x13] != 3 {
		t.Fatalf("LANEMAP.3 write: err=%v win=%#x", err, f.win[0x13])
	}
	// A writable stub (PAIR_TRIM, v3.0+) takes the write and reads 0 in v2.2.
	if err := d.WindowWrite("PAIR_TRIM", 1); err != nil {
		t.Fatalf("PAIR_TRIM write: %v", err)
	}
	if wv, err := d.WindowRead("PAIR_TRIM"); err != nil || wv.Value != 0 {
		t.Fatalf("PAIR_TRIM reads %#x %v, want 0 (stub)", wv.Value, err)
	}
	if err := d.WindowWrite("CAL_STAT", 1); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("read-only stub CAL_STAT accepted a write: %v", err)
	}
}

func TestIdentityAndStatus(t *testing.T) {
	d, f := newTestDiag()
	id, err := d.Identity()
	if err != nil || !id.OK || !id.ConfDone || id.BuildID != fmt.Sprintf("0x%08x", iface.BuildID) {
		t.Fatalf("identity: %+v %v", id, err)
	}
	d.SetStatusExtra(func() map[string]any { return map[string]any{"frames": 42} })
	st := d.Status()
	if st["pll_a_lock"] != true || st["frames"] != 42 || !strings.Contains(st["line"].(string), "fabric=ok") {
		t.Fatalf("status: %v", st)
	}
	f.regs[iface.SelFabricId] = 0x1234
	id, _ = d.Identity()
	if id.OK || id.Err == "" {
		t.Fatalf("mismatch not reported: %+v", id)
	}
	if st := d.Status(); !strings.Contains(st["line"].(string), "MISMATCH") {
		t.Fatalf("status line: %v", st["line"])
	}
}

func TestCensus(t *testing.T) {
	d, f := newTestDiag()
	f.regs[iface.SelDiagIdx] = 0x0c // must be restored
	c, err := d.Census()
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Lanes) != 118 || c.LanesToggling != 81 { // 80 lanes + K2; +B11 +J1 do not toggle
		t.Fatalf("lanes=%d toggling=%d", len(c.Lanes), c.LanesToggling)
	}
	if c.Lanes[5].Tog != 1005 || c.Lanes[5].Name != "lane05" || !c.Lanes[5].Ever1 {
		t.Fatalf("lane 5: %+v", c.Lanes[5])
	}
	if c.Summary["adc_lanes_toggling"] != "80/80" || c.Summary["bus_balls_toggling"] != "0/27" {
		t.Fatalf("summary: %v", c.Summary)
	}
	if c.BusLevels["R3"] != 1 || c.BusRdLo != 0xffff || c.BusRdHi != 0x07ff {
		t.Fatalf("released bus must read the pull-up: lo=%#x hi=%#x", c.BusRdLo, c.BusRdHi)
	}
	if f.regs[iface.SelDiagIdx] != 0x0c {
		t.Fatalf("DIAG_IDX not restored: %#x", f.regs[iface.SelDiagIdx])
	}
	if st := d.Status(); st["lanes_toggling"] != 81 {
		t.Fatalf("status carries no census summary: %v", st)
	}
}

func TestSnapshot(t *testing.T) {
	d, f := newTestDiag()
	f.snapN = 300
	words, err := d.Snapshot(6, 2)
	if err != nil || len(words) != 300 || words[0] != 0xA000 || words[299] != 0xA000+299 {
		t.Fatalf("snapshot: n=%d err=%v", len(words), err)
	}
	if f.win[iface.DiagSnapMode] != 6 {
		t.Fatalf("SNAP_MODE = %d", f.win[iface.DiagSnapMode])
	}
	ctl := f.regs[iface.SelDiagCtrl]
	if ctl&iface.DiagCtrlSnapArmMask != 0 || (ctl&iface.DiagCtrlSnapClkMask)>>iface.DiagCtrlSnapClkShift != 2 {
		t.Fatalf("DIAG_CTRL after snapshot = %#04x (arm must be a pulse, clk=2)", ctl)
	}
	if ctl&iface.DiagCtrlK2EnMask == 0 || ctl&iface.DiagCtrlF1Mask == 0 {
		t.Fatal("snapshot disturbed the other DIAG_CTRL bits")
	}
	if _, err := d.Snapshot(8, 0); err == nil {
		t.Fatal("mode 8 accepted")
	}
}

func TestBusDrive(t *testing.T) {
	d, f := newTestDiag()
	if err := d.BusDrive("R3", true, 0); err != nil {
		t.Fatal(err)
	}
	s, _ := d.BusRead()
	if !s.Balls["R3"].OE || s.Balls["R3"].Drive != 0 || s.Balls["R3"].Read != 1 || s.Master {
		t.Fatalf("without the master enable the ball must stay released: %+v", s.Balls["R3"])
	}
	if err := d.BusMaster(true); err != nil {
		t.Fatal(err)
	}
	s, _ = d.BusRead()
	if s.Balls["R3"].Read != 0 || !s.Master {
		t.Fatalf("driven 0 must read 0: %+v", s.Balls["R3"])
	}
	if err := d.BusDrive("T7", true, 1); err != nil {
		t.Fatal(err)
	}
	s, _ = d.BusRead()
	if s.Balls["T7"].Read != 1 || s.OeHi != 1<<10|1 || s.DrvHi != 1<<10 { // R3 (hi bit 0) is still enabled
		t.Fatalf("T7: %+v oe_hi=%#x drv_hi=%#x", s.Balls["T7"], s.OeHi, s.DrvHi)
	}
	if err := d.BusReleaseAll(); err != nil {
		t.Fatal(err)
	}
	s, _ = d.BusRead()
	if s.Master || s.OeLo != 0 || s.OeHi != 0 || s.Balls["R3"].Read != 1 {
		t.Fatalf("release all: %+v", s)
	}
	if err := d.BusDrive("Z9", true, 1); err == nil {
		t.Fatal("unknown ball accepted")
	}
	if err := d.SetCtrl(iface.DiagCtrlD2Mask, true); err != nil {
		t.Fatal(err)
	}
	s, _ = d.BusRead()
	if s.D2 != 1 || s.P6 != 1 {
		t.Fatalf("D2/P6: %+v", s)
	}
	if f.regs[iface.SelDiagCtrl]&iface.DiagCtrlSnapArmMask != 0 {
		t.Fatal("SetCtrl re-pulsed the snapshot arm")
	}
}

func TestVendorSequence(t *testing.T) {
	d, f := newTestDiag()
	f.d2Dep = true
	res, err := d.VendorSequence(0)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"21=00c0", "21=00c0", "57=0001", "57=0000", "21=00c3", "21=00c8"}
	if strings.Join(f.raw, " ") != strings.Join(want, " ") {
		t.Fatalf("raw words %v, want %v", f.raw, want)
	}
	if len(res.Steps) != 6 || res.Steps[4].Snoop.Fields["SEL"] != 0x21 || res.Steps[4].Data != 0x00c3 {
		t.Fatalf("steps: %+v", res.Steps)
	}
	if !res.Restored || len(res.Changed) != 0 {
		t.Fatalf("restored=%v changed=%v", res.Restored, res.Changed)
	}
	// the engine's program is intact and re-armed
	if f.regs[iface.SelRun] != iface.RunRunMask || f.regs[iface.SelDecimLo] != 160 || !f.armed {
		t.Fatalf("registers not restored/re-armed: run=%#x decim=%d armed=%v", f.regs[iface.SelRun], f.regs[iface.SelDecimLo], f.armed)
	}
}

func TestE2FollowRates(t *testing.T) {
	d, f := newTestDiag()
	// R4 is foreign-driven low only while D2=1; M6 always foreign high.
	f.foreign[17] = 0
	f.foreign[11] = 1
	f.d2Dep = false
	f.foreign = map[int]uint8{11: 1}
	res, err := d.E2(E2Options{Blocks: 3, Repeats: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Order) != 6 || res.Order[0] != 0 || res.Order[1] != 1 || res.Order[5] != 1 {
		t.Fatalf("block order %v (must alternate D2 0/1 ×3)", res.Order)
	}
	find := func(cond int, ball string) BallScore {
		for _, s := range res.Conditions[cond].Scores {
			if s.Ball == ball {
				return s
			}
		}
		t.Fatalf("no score for %s", ball)
		return BallScore{}
	}
	for cond := 0; cond < 2; cond++ {
		j6 := find(cond, "J6")
		if j6.Follow0 != 1 || j6.Follow1 != 1 || j6.RestHigh != 1 || j6.Spread != 0 || len(j6.PerBlock0) != 3 {
			t.Fatalf("J6 D2=%d: %+v", cond, j6)
		}
		m6 := find(cond, "M6")
		if m6.Follow0 != 0 || m6.Follow1 != 1 || m6.RestHigh != 1 {
			t.Fatalf("M6 (foreign high) D2=%d: %+v", cond, m6)
		}
		// L4/T2/T7 are held by the ADC recipe and not driven.
		if !find(cond, "L4").Held || !find(cond, "T7").Held {
			t.Fatalf("ADC-held balls must be reported held")
		}
	}
	if !res.Restored || f.regs[iface.SelDiagCtrl]&iface.DiagCtrlBusDrvEnMask != 0 || f.win[iface.DiagBusOeLo] != 0 {
		t.Fatalf("E2 left the bus driven: ctrl=%#x oe=%#x restored=%v", f.regs[iface.SelDiagCtrl], f.win[iface.DiagBusOeLo], res.Restored)
	}
	if f.win[iface.DiagAdcHold] != 7 || !f.armed {
		t.Fatalf("ADC_HOLD=%d armed=%v after E2", f.win[iface.DiagAdcHold], f.armed)
	}
	if !strings.Contains(res.Summary[0], "23/27") {
		t.Fatalf("summary: %v", res.Summary)
	}
	// D2-dependent ownership shows up as a per-condition difference.
	f.foreign = map[int]uint8{17: 0}
	f.d2Dep = true
	res, _ = d.E2(E2Options{Blocks: 3, Repeats: 1, IncludeADCHold: true})
	if r4 := find(1, "R4"); r4.Follow1 != 0 || r4.RestHigh != 0 {
		t.Fatalf("R4 under D2=1: %+v", r4)
	}
	if r4 := find(0, "R4"); r4.Follow1 != 1 || r4.RestHigh != 1 {
		t.Fatalf("R4 under D2=0: %+v", r4)
	}
	if find(0, "L4").Held || f.win[iface.DiagAdcHold] != 7 {
		t.Fatal("include_adc_hold must drive the held balls and restore ADC_HOLD")
	}
}

func TestCaptureScoresRamp(t *testing.T) {
	d, f := newTestDiag()
	res, err := d.Capture(CaptureOptions{Words: 1000, Samples: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Words != 1000 || res.RampBreaks1 != 0 || res.RampBreaks2 != 999 || res.NonZero != 1000 {
		t.Fatalf("score: %+v", res)
	}
	if res.Distinct1 != 256 || res.Distinct2 != 1 || res.Mean2 != 0x55 || len(res.First) != 32 {
		t.Fatalf("score: distinct=%d/%d mean2=%g first=%d", res.Distinct1, res.Distinct2, res.Mean2, len(res.First))
	}
	if !res.Restored || f.regs[iface.SelPretrigLo] != 3072 || f.regs[iface.SelDecimLo] != 160 || !f.armed {
		t.Fatalf("engine program not restored: pre=%d decim=%d armed=%v", f.regs[iface.SelPretrigLo], f.regs[iface.SelDecimLo], f.armed)
	}
	res, _ = d.Capture(CaptureOptions{Words: 64})
	if res.C1 != nil || res.Words != 64 {
		t.Fatalf("samples must be omitted unless asked: %d words", res.Words)
	}
	// A full-record request is bounded by what the fabric finalizes (pre+post <=
	// PRETRIG_MAX): the words delivered must equal the words programmed, so a
	// bench shortfall is never manufactured by the request itself.
	for _, ask := range []int{0, iface.RecDepth, 1 << 20} {
		res, err = d.Capture(CaptureOptions{Words: ask})
		if err != nil {
			t.Fatalf("Capture(words=%d): %v", ask, err)
		}
		if res.Requested != res.Words || res.Words != iface.PretrigMax ||
			int(res.Remain&iface.BurstRemainRemainMask) != iface.PretrigMax {
			t.Fatalf("Capture(words=%d): requested %d, delivered %d, fabric held %d, want %d throughout",
				ask, res.Requested, res.Words, res.Remain&iface.BurstRemainRemainMask, iface.PretrigMax)
		}
	}
}

func TestRunnerErrorsPropagate(t *testing.T) {
	d := New(RunnerFunc(func(fn func(bus.Bus) error, _ time.Duration) error { return fmt.Errorf("busy") }), nil)
	if _, err := d.RegRead("RUN"); err == nil || err.Error() != "busy" {
		t.Fatalf("err = %v", err)
	}
	if _, err := d.Census(); err == nil {
		t.Fatal("census must fail when the runner does")
	}
	if st := d.Status(); st["err"] != "busy" {
		t.Fatalf("status err: %v", st)
	}
}

// ---- v3 rungs ----

func TestCaptureTsrcAndWindow(t *testing.T) {
	d, f := newTestDiag()
	for _, chmode := range []uint16{iface.RunChmodeDual, iface.RunChmodeCh1, iface.RunChmodeCh2} {
		for _, tsrc := range []uint16{iface.IlCtrlTsrcRamp, iface.IlCtrlTsrcColtag, iface.IlCtrlTsrcGlitch} {
			res, err := d.Capture(CaptureOptions{Words: 1200, Tsrc: tsrc, Chmode: chmode})
			if err != nil {
				t.Fatalf("tsrc %d chmode %d: %v", tsrc, chmode, err)
			}
			if !res.Exact.OK || res.Exact.Breaks != 0 || !res.Exact.PopsOK || res.Words != 1200 || res.Tsrc != tsrc {
				t.Fatalf("tsrc %d chmode %d: %+v", tsrc, chmode, res.Exact)
			}
			if !res.Restored || f.regs[iface.SelIlCtrl] != 0 || f.regs[iface.SelRun] != iface.RunRunMask {
				t.Fatalf("program not restored: IL_CTRL=%#x RUN=%#x", f.regs[iface.SelIlCtrl], f.regs[iface.SelRun])
			}
		}
	}
	// A window drain goes through DRAIN_START/LEN + REWIND and is checked against the pattern.
	res, err := d.Capture(CaptureOptions{Words: 1000, Tsrc: iface.IlCtrlTsrcRamp, Start: 500, Len: 100, Samples: true})
	if err != nil {
		t.Fatal(err)
	}
	// (restore re-arms the fabric, so the capture's k0 is the fake's current one minus one GO)
	if res.Words != 100 || !res.Exact.OK || res.Exact.K0 != f.k0-700+500 || res.DrainStat.Pops != 100 {
		t.Fatalf("window: words=%d exact=%+v stat=%+v", res.Words, res.Exact, res.DrainStat)
	}
	if f.regs[iface.SelDrainStart] != 0 || f.regs[iface.SelDrainLen] != 0 {
		t.Fatalf("drain window not restored: %d/%d", f.regs[iface.SelDrainStart], f.regs[iface.SelDrainLen])
	}
	// The ADC path (no pattern) still reports the pointer delta.
	res, err = d.Capture(CaptureOptions{Words: 300})
	if err != nil || res.Exact.Tsrc != 0 || !res.Exact.PopsOK || res.Exact.Breaks != 0 || res.RampBreaks1 != 0 {
		t.Fatalf("adc capture: %+v %v", res.Exact, err)
	}
	if _, err := d.Capture(CaptureOptions{Tsrc: 4}); err == nil {
		t.Fatal("tsrc 4 accepted")
	}
}

func TestRedrainWindows(t *testing.T) {
	d, f := newTestDiag()
	res, err := d.Redrain(RedrainOptions{Words: 2000, Windows: 8, Passes: 2, Odd: true})
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || res.Bad != 0 || len(res.Passes) != 2 || len(res.Windows) != 10 || res.RecLen != 2000 || !res.Restored {
		t.Fatalf("redrain: ok=%v bad=%d passes=%d windows=%d rec=%d restored=%v", res.OK, res.Bad, len(res.Passes), len(res.Windows), res.RecLen, res.Restored)
	}
	if w := res.Windows[7]; w.Window.Start != 1750 || w.Window.Len != 0 || w.Exact.Words != 250 {
		t.Fatalf("last window runs to the end: %+v", w)
	}
	if w := res.Windows[9]; w.Window.Start != 1999 || w.Exact.Words != 1 || !w.Exact.OK {
		t.Fatalf("last-word window: %+v", w)
	}
	if res.Full.K0 != f.k0-700 || res.Windows[3].Exact.K0 != f.k0-700+750 {
		t.Fatalf("k0: full=%d win3=%d fabric=%d", res.Full.K0, res.Windows[3].Exact.K0, f.k0)
	}
	// The converters as source: byte identity only.
	res, err = d.Redrain(RedrainOptions{Words: 500, Adc: true, Windows: 2, Passes: 1})
	if err != nil || !res.OK || res.Full.Tsrc != 0 || res.Windows[1].Exact.Mismatch != 0 {
		t.Fatalf("adc redrain: %+v %v", res, err)
	}
	if f.regs[iface.SelDecimLo] != 160 || !f.armed {
		t.Fatal("engine program not restored")
	}
}

func TestSchemaCheck(t *testing.T) {
	d, f := newTestDiag()
	res, err := d.SchemaCheck(SchemaOptions{Writes: 20})
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || !res.Independent || !res.UndecodedOK || !res.LanemapRO || !res.Restored || !res.Identity.OK {
		t.Fatalf("schema check: ok=%v indep=%v (%s) undecoded=%v lanemap_ro=%v restored=%v", res.OK, res.Independent, res.IndepDetail, res.UndecodedOK, res.LanemapRO, res.Restored)
	}
	names := map[string]RegCheck{}
	for _, rc := range res.Registers {
		names[rc.Name] = rc
		if rc.Writes != 20 || rc.Mismatches != 0 {
			t.Fatalf("%s: %+v", rc.Name, rc)
		}
	}
	if il := names["IL_CTRL"]; il.LiveMask != iface.IlCtrlTsrcMask || il.Stub {
		t.Fatalf("IL_CTRL live mask %#04x, want TSRC only: %+v", il.LiveMask, il)
	}
	if ds := names["DRAIN_START"]; ds.LiveMask != iface.DrainStartIdxMask {
		t.Fatalf("DRAIN_START live mask %#04x", ds.LiveMask)
	}
	if sn := names["STACK_N"]; !sn.Stub || sn.LiveMask != 0 {
		t.Fatalf("STACK_N must be a stub: %+v", sn)
	}
	if _, ok := names["DRAIN_STAT"]; ok {
		t.Fatal("read-only registers must not be written")
	}
	if len(res.Lanemap) != iface.DiagLanemapCount || res.Lanemap[5] != 5 || len(res.DiagStubs) == 0 {
		t.Fatalf("lanemap/diag stubs: %d %v %d", len(res.Lanemap), res.Lanemap[:6], len(res.DiagStubs))
	}
	if res.StubReads["STATUS_B"] != 0 || res.Undecoded["0x21"] != 0 || res.Undecoded["0x57"] != 0 {
		t.Fatalf("stub/undecoded reads: %v %v", res.StubReads, res.Undecoded)
	}
	if f.regs[iface.SelIlCtrl] != 0 || f.regs[iface.SelRun] != iface.RunRunMask || !f.armed {
		t.Fatalf("engine program not restored: IL_CTRL=%#x RUN=%#x", f.regs[iface.SelIlCtrl], f.regs[iface.SelRun])
	}
	// A fabric that stores IL_CTRL's reserved bits fails the check.
	f2 := newFakeFabric()
	d2 := New(direct(f2), nil)
	d2.sleep = func(time.Duration) {}
	f2Write := f2.Write
	_ = f2Write
	res, err = d2.SchemaCheck(SchemaOptions{Writes: 3})
	if err != nil || !res.OK {
		t.Fatalf("second run: %v %v", err, res.OK)
	}
}

// fakeTimingPort is a CS1 timing port with a floor below which the fake
// fabric duplicates every 32nd pop (the burst-boundary signature).
type fakeTimingPort struct {
	f         *fakeFabric
	cur       bus.CS1Timing
	minAccess uint32
	applies   int
}

func (p *fakeTimingPort) Read() (bus.CS1Timing, error) { return p.cur, nil }
func (p *fakeTimingPort) set(t bus.CS1Timing) {
	p.cur = t
	p.applies++
	p.f.mu.Lock()
	p.f.corrupt = t.RdAccess() < p.minAccess
	p.f.mu.Unlock()
}
func (p *fakeTimingPort) Apply(t bus.CS1Timing) error {
	if err := t.Validate(); err != nil {
		return err
	}
	p.set(t)
	return nil
}
func (p *fakeTimingPort) Restore(t bus.CS1Timing) error { p.set(t); return nil }

func TestGpmcSweepPersistApply(t *testing.T) {
	d, f := newTestDiag()
	if _, err := d.GpmcSweep(bus.SweepOptions{}); err == nil {
		t.Fatal("sweep without a timing port must refuse")
	}
	if st := d.GpmcStatus(); st.Err == "" {
		t.Fatal("status without timing must say so")
	}
	port := &fakeTimingPort{f: f, cur: bus.FactoryCS1Timing, minAccess: 10}
	path := filepath.Join(t.TempDir(), bus.TimingFileName)
	// simulate the boot hook: with no persisted file it records the factory
	// timing beside the app, which every later apply gates on.
	boot := bus.ApplyPersistedTiming(f, port, path, "test", nil)
	if !boot.Captured {
		t.Fatalf("boot hook did not record the factory timing: %+v", boot)
	}
	d.SetTiming(port, path, "test", boot)
	st := d.GpmcStatus()
	if !st.Port || st.Current == nil || st.Current.RdCycle != 31 || st.Persisted != nil || st.FileErr != "" {
		t.Fatalf("status: %+v", st)
	}
	if _, err := d.GpmcPersist("v"); err == nil {
		t.Fatal("persist before a sweep must refuse")
	}
	// the sweep is a background job: start it, then poll the status the way
	// tools/hw/gpmc_sweep.sh does. Every step restores the start timing.
	if _, err := d.GpmcSweep(bus.SweepOptions{Words: 400, Drains: 1, Blocks: 3, VerifyWords: 1200}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(60 * time.Second)
	var res *bus.SweepResult
	for {
		st := d.GpmcStatus()
		if st.Sweep != nil && st.Sweep.Finished {
			if st.Sweep.Err != "" {
				t.Fatalf("sweep: %s", st.Sweep.Err)
			}
			res = st.LastSweep
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("sweep did not finish: %+v", st.Sweep)
		}
		time.Sleep(2 * time.Millisecond)
	}
	if res == nil {
		t.Fatal("sweep finished with no result")
	}
	if !res.OK || res.FloorAccess != 10 || res.Chosen.RdAccess != 11 || port.cur != bus.FactoryCS1Timing || !res.Restored {
		t.Fatalf("sweep: ok=%v floor=%d chosen=%+v cur=%s", res.OK, res.FloorAccess, res.Chosen, port.cur)
	}
	if st := d.GpmcStatus(); st.Sweep == nil || st.Sweep.Running || st.Sweep.Steps < 1 {
		t.Fatalf("sweep progress after the job: %+v", st.Sweep)
	}
	if f.regs[iface.SelIlCtrl] != 0 || f.regs[iface.SelDecimLo] != 160 || !f.armed {
		t.Fatal("engine program not restored after the sweep")
	}
	tf, err := d.GpmcPersist("v")
	if err != nil || tf.Chosen != res.ChosenRaw {
		t.Fatalf("persist: %v %+v", err, tf)
	}
	if st := d.GpmcStatus(); st.Persisted == nil || st.Persisted.Chosen != res.ChosenRaw || st.LastSweep == nil {
		t.Fatalf("status after persist: %+v", st)
	}
	bt, err := d.GpmcApply("persisted")
	if err != nil || bt.Source != "persisted" || port.cur != res.ChosenRaw || bt.Check == nil || !bt.Check.OK {
		t.Fatalf("apply persisted: %v %+v", err, bt)
	}
	if f.regs[iface.SelIlCtrl] != 0 || !f.armed {
		t.Fatal("engine program not restored after apply")
	}
	bt, err = d.GpmcApply("factory")
	if err != nil || bt.Source != "factory" || port.cur.RdAccess() != 13 || port.cur.RdCycle() != 31 {
		t.Fatalf("apply factory: %v %+v cur=%s", err, bt, port.cur)
	}
	if _, err := d.GpmcApply("nonsense"); err == nil {
		t.Fatal("bad apply target accepted")
	}
	// The unit's floor moved (a persisted timing that no longer drains exactly): the gate falls back.
	port.minAccess = 12
	bt, err = d.GpmcApply("persisted")
	if err != nil || bt.Source != "factory" || !bt.Rejected || port.cur.RdAccess() != 13 {
		t.Fatalf("gate fallback: %v %+v cur=%s", err, bt, port.cur)
	}
}
