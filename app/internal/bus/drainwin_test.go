package bus

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"open-sds/app/internal/iface"
)

// fakeFab models the v2.2 fabric's capture + drain side: a record filled by
// the TSRC source on GO (k = the writer's word index since GO), frozen by
// HALT, drained through the DRAIN_START/LEN window with REWIND, DRAIN_STAT
// pops and POP_MON underruns; and a "timing floor" hook that corrupts pops
// (a duplicate every 32nd word — the EDMA burst-boundary signature) when the
// GPMC timing applied through the fake port is below the floor.
type fakeFab struct {
	mu      sync.Mutex
	regs    map[uint16]uint16
	armed   bool
	halted  bool
	rec     int    // finalized words
	k0      uint32 // writer index of record word 0
	tsrc    uint16
	winS    int // latched window
	winL    int
	ptr     int
	pops    uint16
	under   uint8
	last    uint16
	corrupt bool // pops misbehave (timing below the floor)
	dupped  bool // the last pop was the injected duplicate
	goCount int
}

func newFakeFab() *fakeFab {
	f := &fakeFab{regs: map[uint16]uint16{}}
	f.regs[iface.SelAcqCtrl] = 0x7c00
	return f
}

func (f *fakeFab) window() (start, n int) {
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

func (f *fakeFab) latch() {
	f.winS = int(f.regs[iface.SelDrainStart] & iface.DrainStartIdxMask)
	f.winL = int(f.regs[iface.SelDrainLen])
	f.ptr = 0
	f.pops = 0
}

func (f *fakeFab) word(i int) uint16 {
	w, _ := iface.TsrcWord(f.tsrc, f.k0+uint32(i))
	if f.tsrc == iface.IlCtrlTsrcAdc {
		w = uint16(0x80<<8 | (i & 0xff))
	}
	return w
}

func (f *fakeFab) pop() uint16 {
	start, n := f.window()
	f.pops++
	if !f.halted || f.ptr >= n {
		f.under++
		return f.last
	}
	i := start + f.ptr
	f.ptr++
	if f.corrupt && f.ptr%32 == 0 && !f.dupped { // duplicate: the pointer did not advance
		f.ptr--
		f.dupped = true
	} else {
		f.dupped = false
	}
	f.last = f.word(i)
	return f.last
}

func (f *fakeFab) Read(plane uint8, sel uint16) (uint16, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if plane != PlaneCS1 {
		return 0, nil
	}
	switch iface.MaskSel(sel) {
	case iface.SelStatusA:
		if f.armed {
			return iface.StatusADoneMask | iface.StatusAValidMask, nil
		}
		return 0, nil
	case iface.SelFill:
		return uint16(f.rec), nil
	case iface.SelBurstRemain:
		if !f.halted {
			return 0, nil
		}
		_, n := f.window()
		return iface.BurstRemainReadyMask | uint16(n-f.ptr), nil
	case iface.SelBurst, iface.SelBurstAlias:
		return f.pop(), nil
	case iface.SelDrainStat:
		return f.pops, nil
	case iface.SelPopMon:
		return uint16(f.under)<<iface.PopMonUnderrunShift | 7, nil
	}
	return f.regs[iface.MaskSel(sel)], nil
}

func (f *fakeFab) Write(plane uint8, sel, val uint16) error {
	if !Writable(plane, sel) {
		return fmt.Errorf("fake: not writable cs%d %#04x", plane, sel)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if plane != PlaneCS1 {
		return nil
	}
	sel = iface.MaskSel(sel)
	if sel == iface.SelOpcode {
		switch val {
		case iface.OpGo:
			f.armed, f.halted = true, false
			f.goCount++
			f.tsrc = (f.regs[iface.SelIlCtrl] & iface.IlCtrlTsrcMask) >> iface.IlCtrlTsrcShift
			pre, post := int(f.regs[iface.SelPretrigLo]), int(f.regs[iface.SelPosttrigLo])
			f.rec = pre + post
			if f.rec > iface.PretrigMax {
				f.rec = iface.PretrigMax
			}
			f.k0 = uint32(1000*f.goCount + 17) // the record starts mid-pattern
			f.under, f.pops = 0, 0
		case iface.OpHalt:
			f.halted = true
			f.latch()
		case iface.OpReset:
			f.armed, f.halted, f.rec = false, false, 0
		case iface.OpRewind:
			f.latch()
		}
		return nil
	}
	f.regs[sel] = val
	return nil
}

func (f *fakeFab) RawWrite(sel, val uint16) error { return nil }
func (f *fakeFab) BurstInto(c1, c2 []uint8, n int) {
	for i := 0; i < n; i++ {
		v, _ := f.Read(PlaneCS1, iface.SelBurst)
		c1[i], c2[i] = iface.Split(v)
	}
}
func (f *fakeFab) PopWords(sel uint16, dst []uint16, n int) {
	for i := 0; i < n; i++ {
		dst[i], _ = f.Read(PlaneCS1, sel)
	}
}
func (f *fakeFab) FastDrain() bool { return true }

func noSleep(time.Duration) {}

func TestCaptureTsrcAndWindowedDrain(t *testing.T) {
	f := newFakeFab()
	rec, err := CaptureTsrc(f, TsrcOptions{Words: 1000, Tsrc: iface.IlCtrlTsrcRamp}, noSleep)
	if err != nil || rec != 1000 {
		t.Fatalf("CaptureTsrc: rec=%d err=%v", rec, err)
	}
	if f.regs[iface.SelIlCtrl] != iface.IlCtrlTsrcRamp<<iface.IlCtrlTsrcShift {
		t.Fatalf("IL_CTRL = %#04x, want TSRC=RAMP", f.regs[iface.SelIlCtrl])
	}
	full := make([]uint16, 1000)
	r, err := DrainWindow(f, Full, full)
	if err != nil || r.N != 1000 {
		t.Fatalf("full drain: n=%d err=%v", r.N, err)
	}
	e := CheckExact(r, full[:r.N], iface.IlCtrlTsrcRamp, nil)
	if !e.OK || e.K0 != f.k0 || e.PopsDelta != 1000 {
		t.Fatalf("full drain exactness: %+v", e)
	}
	// Eight windows of the frozen record are byte-identical to their slices (R3).
	for i := 0; i < 8; i++ {
		w := Window{Start: uint16(i * 120), Len: 100}
		dst := make([]uint16, 100)
		r, err := DrainWindow(f, w, dst)
		if err != nil || r.N != 100 {
			t.Fatalf("window %v: n=%d err=%v", w, r.N, err)
		}
		e := CheckExact(r, dst, iface.IlCtrlTsrcRamp, full[w.Start:int(w.Start)+100])
		if !e.OK || e.K0 != f.k0+uint32(w.Start) {
			t.Fatalf("window %v: %+v", w, e)
		}
	}
	// LEN 0 = to the end; a short dst pops only what fits and the pop delta says so.
	dst := make([]uint16, 50)
	r, err = DrainWindow(f, Window{Start: 980}, dst)
	if err != nil || r.N != 20 {
		t.Fatalf("tail window: n=%d err=%v", r.N, err)
	}
	if e := CheckExact(r, dst[:20], iface.IlCtrlTsrcRamp, full[980:]); !e.OK {
		t.Fatalf("tail window: %+v", e)
	}
	// An over-drain (pops past the window) is caught by the underrun counter.
	if err := SetWindow(f, Window{Start: 990}); err != nil {
		t.Fatal(err)
	}
	_ = Rewind(f)
	before, _ := ReadDrainStat(f)
	over := make([]uint16, 12)
	f.PopWords(iface.SelBurst, over, 12)
	after, _ := ReadDrainStat(f)
	e = CheckExact(DrainResult{N: 12, Before: before, After: after}, over, iface.IlCtrlTsrcRamp, nil)
	if e.OK || e.Underruns != 2 || !e.PopsOK || e.Breaks != 2 {
		t.Fatalf("over-drain: %+v", e)
	}
	// A window beyond the record is refused before any write.
	if err := SetWindow(f, Window{Start: iface.PretrigMax + 1}); err == nil {
		t.Fatal("window start beyond the record accepted")
	}
	// The channel split form.
	c1, c2 := make([]uint8, 10), make([]uint8, 10)
	if r, err := DrainWindowInto(f, Window{Start: 3, Len: 10}, c1, c2, make([]uint16, 10)); err != nil || r.N != 10 {
		t.Fatalf("DrainWindowInto: %+v %v", r, err)
	}
	if w := iface.Word(c1[0], c2[0]); w != full[3] {
		t.Fatalf("split word %#04x, want %#04x", w, full[3])
	}
}

func TestCheckExactVerdicts(t *testing.T) {
	words := []uint16{5, 6, 7, 8}
	r := DrainResult{N: 4, Before: DrainStat{Pops: 0xfffe}, After: DrainStat{Pops: 2}}
	if e := CheckExact(r, words, iface.IlCtrlTsrcRamp, nil); !e.OK || !e.PopsOK || e.K0 != 5 {
		t.Fatalf("wrapping pop counter: %+v", e)
	}
	r.After.Pops = 3
	if e := CheckExact(r, words, iface.IlCtrlTsrcRamp, nil); e.OK || e.PopsOK {
		t.Fatalf("pop mismatch not flagged: %+v", e)
	}
	r.After.Pops = 2
	if e := CheckExact(r, []uint16{5, 6, 6, 7}, iface.IlCtrlTsrcRamp, nil); e.OK || e.Breaks != 2 || e.FirstBad != 2 {
		t.Fatalf("ramp break not flagged: %+v", e)
	}
	if e := CheckExact(r, words, iface.IlCtrlTsrcAdc, []uint16{5, 6, 9, 8}); e.OK || e.Mismatch != 1 || e.FirstDiff != 2 {
		t.Fatalf("reference mismatch not flagged: %+v", e)
	}
	if e := CheckExact(r, words, iface.IlCtrlTsrcAdc, []uint16{5}); e.OK || e.Mismatch != 4 {
		t.Fatalf("reference length mismatch not flagged: %+v", e)
	}
	r.After.Underrun = 1
	if e := CheckExact(r, words, iface.IlCtrlTsrcAdc, nil); e.OK || e.Underruns != 1 {
		t.Fatalf("underrun not flagged: %+v", e)
	}
	// COLTAG and GLITCH go through the same path.
	col := []uint16{0x0207, 0x0307, 0x0407, 0x0008}
	r = DrainResult{N: 4, After: DrainStat{Pops: 4}}
	if e := CheckExact(r, col, iface.IlCtrlTsrcColtag, nil); !e.OK || e.K0 != 7*5+2 {
		t.Fatalf("coltag: %+v", e)
	}
}

func TestRampCheckPassesAndDetectsCorruption(t *testing.T) {
	f := newFakeFab()
	rep, err := RampCheck(f, TsrcOptions{Words: 2000}, 4, noSleep)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.OK || rep.Passes != 4 || rep.Words != 8000 || rep.RecLen != 2000 || rep.Bad != 0 {
		t.Fatalf("clean check: %+v", rep)
	}
	f.corrupt = true
	rep, err = RampCheck(f, TsrcOptions{Words: 2000, Tsrc: iface.IlCtrlTsrcColtag}, 2, noSleep)
	if err != nil {
		t.Fatal(err)
	}
	// The reference drain itself is corrupt (pattern breaks) and the re-drains
	// differ from it: everything counts.
	if rep.OK || rep.Bad == 0 || rep.Breaks == 0 || rep.FirstBad == nil {
		t.Fatalf("corrupt check not detected: %+v", rep)
	}
	// After ResetTsrc the fabric is idle with the ADC source and the full window.
	if err := ResetTsrc(f); err != nil {
		t.Fatal(err)
	}
	if f.regs[iface.SelIlCtrl] != 0 || f.regs[iface.SelDrainLen] != 0 || f.armed || f.regs[iface.SelAcqCtrl] != 0x7c00 {
		t.Fatalf("ResetTsrc left IL_CTRL=%#x DRAIN_LEN=%d armed=%v ACQ_CTRL=%#x", f.regs[iface.SelIlCtrl], f.regs[iface.SelDrainLen], f.armed, f.regs[iface.SelAcqCtrl])
	}
}

func TestRampCheckNeedsARecord(t *testing.T) {
	f := newFakeFab()
	f.regs[iface.SelPretrigLo] = 0 // GO with pre+post 0 finalizes nothing
	dst := make([]uint16, 4)
	if _, err := DrainWindow(f, Full, dst); err == nil {
		t.Fatal("drain without a frozen record must fail")
	}
}
