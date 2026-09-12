package bus

import (
	"testing"

	"open-sds/app/internal/iface"
)

func TestEncode(t *testing.T) {
	// Verified encodings (ota gpmc_test.go): plane, raw selector little-endian,
	// value little-endian, never pre-shifted.
	if got := encode(1, 0x18, 0); got != [6]byte{1, 0, 0x18, 0, 0, 0} {
		t.Fatalf("version read encode = %v", got)
	}
	if got := encode(3, 0x0134, 0xBEEF); got != [6]byte{3, 0, 0x34, 0x01, 0xEF, 0xBE} {
		t.Fatalf("cs3 write encode = %v", got)
	}
}

// TestWritableIsSchemaDerived: every CS1 register the schema marks R (identity,
// status, pop ports, snoop) is refused; every RW/W register passes; undefined
// selectors (only bits outside the mask make one) are refused; CS3 passes
// except the configuration port.
func TestWritableIsSchemaDerived(t *testing.T) {
	for _, r := range iface.Registers() {
		if got := Writable(PlaneCS1, r.Sel); got != r.Access.CanWrite() {
			t.Errorf("%s (%#04x): Writable=%v, schema access %v", r.Name, r.Sel, got, r.Access)
		}
	}
	// The vendor arm/halt words: schema v3 keeps 0x21 and 0x57 undecoded
	// (read 0, writes ignored) so the E1 replay goes through RawWrite only;
	// the guard refuses them like any undefined selector. Bit 7 is still
	// masked (0xa4 → RUN); every v2 register keeps its selector.
	for _, u := range iface.Undecoded() {
		if Writable(PlaneCS1, u) {
			t.Errorf("undecoded %#04x must be refused by Write (RawWrite is the E1 path)", u)
		}
	}
	if !Writable(PlaneCS1, 0xa4) || !Writable(PlaneCS1, iface.SelIlCtrl) || !Writable(PlaneCS1, iface.SelDrainStart) {
		t.Error("bit 7 alias of RUN / the v3 odd selectors must be writable")
	}
	if Writable(PlaneCS1, iface.SelDrainStat) || Writable(PlaneCS1, iface.SelPopMon) {
		t.Error("DRAIN_STAT / POP_MON are read-only")
	}
	if Writable(PlaneCS3, CS3ConfigPort) {
		t.Error("CS3 0x07 (configuration port) must never be writable through the bus")
	}
	for _, sel := range []uint16{0x09, 0x0a, 0x0b, 0x10, 0x14, 0x30, 0x34} { // LED latch, DACs
		if !Writable(PlaneCS3, sel) {
			t.Errorf("CS3 %#04x (MAX V front end) must be writable", sel)
		}
	}
	if Writable(0, iface.SelRun) || Writable(2, iface.SelRun) {
		t.Error("planes other than CS1/CS3 must be refused")
	}
}

func TestNewDoesNotProbe(t *testing.T) {
	// Construction must not touch the fabric (it may still hold the factory
	// image at boot); only a negative fd is refused.
	if _, err := New(-1); err == nil {
		t.Fatal("New(-1) must fail")
	}
	d, err := New(0)
	if err != nil {
		t.Fatalf("New(0): %v", err)
	}
	if d.FastDrain() {
		t.Fatal("FastDrain before EnableEDMA")
	}
	if err := d.Write(0, iface.SelRun, 0); err == nil {
		t.Fatal("plane 0 write must be refused before any syscall")
	}
	if _, err := d.Read(2, iface.SelRun); err == nil {
		t.Fatal("plane 2 read must be refused before any syscall")
	}
}

// ---- EDMA programming against a fake TPCC + fake pager ----

// fakeCC models the parts of the TPCC the drainer uses: a register file, the
// shadow-region trigger that "completes" a transfer by filling the destination
// page with a pattern derived from the running source word counter, and IPR.
type fakeCC struct {
	regs       map[uint32]uint32
	pages      map[uint32][]byte // phys → page slice (from the fake pager)
	srcWord    uint16            // the fabric's pop counter: word i = i (ramp)
	transfers  []struct{ src, dst, bcnt uint32 }
	noComplete bool
	writes     []uint32 // offsets written, in order
}

func newFakeCC() *fakeCC {
	return &fakeCC{regs: map[uint32]uint32{}, pages: map[uint32][]byte{}}
}

func (f *fakeCC) R(off uint32) uint32 { return f.regs[off] }

func (f *fakeCC) W(off, v uint32) {
	f.writes = append(f.writes, off)
	const ch = uint32(edmaChan)
	cb := uint32(1) << (ch % 32)
	switch off {
	case offShadow0 + offICR + 4:
		f.regs[offShadow0+offIPR+4] &^= v
		return
	case offShadow0 + offESR + 4:
		if v&cb != 0 && !f.noComplete {
			pb := uint32(offPaRAM + ch*paramSize)
			src, dst := f.regs[pb+4], f.regs[pb+0xC]
			bcnt := f.regs[pb+8] >> 16
			f.transfers = append(f.transfers, struct{ src, dst, bcnt uint32 }{src, dst, bcnt})
			pg, ok := f.pages[dst]
			if ok {
				for i := uint32(0); i < bcnt; i++ {
					w := f.srcWord
					f.srcWord++
					pg[i*2] = byte(w)
					pg[i*2+1] = byte(w >> 8)
				}
			}
			f.regs[offShadow0+offIPR+4] |= cb
		}
		return
	}
	f.regs[off] = v
}

type fakePager struct {
	cc     *fakeCC
	allocs int
	frees  int
}

func (p *fakePager) alloc(nbytes int) (*dmaBuf, error) {
	nbytes = (nbytes + pageSize - 1) &^ (pageSize - 1)
	buf := make([]byte, nbytes)
	n := nbytes / pageSize
	phys := make([]uint32, n)
	for i := 0; i < n; i++ {
		phys[i] = 0x8000_0000 + uint32(p.allocs)<<20 + uint32(i)*0x3000 // non-contiguous pages
		p.cc.pages[phys[i]] = buf[i*pageSize : (i+1)*pageSize]
	}
	p.allocs++
	return &dmaBuf{buf: buf, phys: phys, release: func() { p.frees++ }}, nil
}

func newFakeDrainer(maxWords int) (*edmaDrainer, *fakeCC, *fakePager) {
	cc := newFakeCC()
	pg := &fakePager{cc: cc}
	return &edmaDrainer{cc: cc, pg: pg, maxWords: maxWords, pollMax: 10}, cc, pg
}

func TestRunParamProgramsChannel40(t *testing.T) {
	d, cc, _ := newFakeDrainer(20480)
	if !d.runParam(burstPortPhys, 0x80001000, 2048) {
		t.Fatal("runParam did not complete")
	}
	const ch = uint32(edmaChan)
	pb := uint32(offPaRAM + ch*paramSize)
	want := map[uint32]uint32{
		pb + 0x00:        optSyncAB | ch<<12 | optTCINTEN,
		pb + 0x04:        burstPortPhys,
		pb + 0x08:        2 | 2048<<16,
		pb + 0x0C:        0x80001000,
		pb + 0x10:        2 << 16,
		pb + 0x14:        linkNone,
		pb + 0x18:        0,
		pb + 0x1C:        1,
		offDCHMAP + ch*4: ch << 5,
	}
	for off, v := range want {
		if got := cc.regs[off]; got != v {
			t.Errorf("reg %#06x = %#08x, want %#08x", off, got, v)
		}
	}
	if cc.regs[offDRAEH0]&(1<<(ch-32)) == 0 {
		t.Error("DRAEH0 bit for channel 40 not granted")
	}
	if burstPortPhys != 0x01000080 {
		t.Errorf("BURST port phys = %#08x, want CS1 base + (0x40<<1)", burstPortPhys)
	}
	// Order: PaRAM, DCHMAP, DRAE, then ICR → EESR → ESR in the shadow region.
	seq := []uint32{offShadow0 + offICR + 4, offShadow0 + offEESR + 4, offShadow0 + offESR + 4}
	idx := 0
	for _, w := range cc.writes {
		if idx < len(seq) && w == seq[idx] {
			idx++
		}
	}
	if idx != len(seq) {
		t.Errorf("trigger sequence ICR→EESR→ESR not observed in order: %v", cc.writes)
	}
	if cc.regs[offShadow0+offIPR+4] != 0 {
		t.Error("IPR not cleared after completion")
	}
}

func TestRunParamTimeout(t *testing.T) {
	d, cc, _ := newFakeDrainer(20480)
	cc.noComplete = true
	if d.runParam(burstPortPhys, 0x80001000, 16) {
		t.Fatal("runParam reported completion without IPR")
	}
}

func TestDrainSplitsPagesAndBytes(t *testing.T) {
	d, cc, pg := newFakeDrainer(20480)
	const n = 5000 // 2048 + 2048 + 904
	c1 := make([]uint8, n)
	c2 := make([]uint8, n)
	if !d.drain(c1, c2, n) {
		t.Fatal("drain failed")
	}
	if len(cc.transfers) != 3 {
		t.Fatalf("transfers = %d, want 3 (one per physical page)", len(cc.transfers))
	}
	wantB := []uint32{2048, 2048, 904}
	for i, tr := range cc.transfers {
		if tr.bcnt != wantB[i] || tr.src != burstPortPhys {
			t.Errorf("transfer %d: src=%#x bcnt=%d, want src=%#x bcnt=%d", i, tr.src, tr.bcnt, burstPortPhys, wantB[i])
		}
	}
	if cc.transfers[0].dst == cc.transfers[1].dst {
		t.Error("consecutive transfers must target distinct physical pages")
	}
	// The pop counter advanced continuously across pages: word i == i, hi byte → C1.
	for i := 0; i < n; i++ {
		w := uint16(i)
		if c1[i] != uint8(w>>8) || c2[i] != uint8(w) {
			t.Fatalf("word %d: c1=%d c2=%d, want %d/%d", i, c1[i], c2[i], w>>8, uint8(w))
		}
	}
	// No /dev/dcinv → a fresh buffer per drain, released afterwards.
	if pg.allocs != 1 || pg.frees != 1 {
		t.Errorf("fresh-buffer path: allocs=%d frees=%d, want 1/1", pg.allocs, pg.frees)
	}
	if d.drain(c1, c2, 20481) {
		t.Error("drain beyond maxWords must be refused")
	}
}

func TestDrainWordsPersistentWithInvalidate(t *testing.T) {
	d, cc, pg := newFakeDrainer(4096)
	buf, _ := pg.alloc(4096 * 2)
	d.persistent = buf
	invalidated := 0
	d.inv = func(b []byte) { invalidated += len(b) }
	dst := make([]uint16, 3000)
	if !d.drainWords(burstPortPhys, dst, 3000) {
		t.Fatal("drainWords failed")
	}
	if invalidated != 6000 {
		t.Errorf("invalidated %d bytes, want 6000", invalidated)
	}
	if pg.allocs != 1 {
		t.Errorf("persistent path allocated %d buffers, want the one at setup", pg.allocs)
	}
	for i, w := range dst {
		if w != uint16(i) {
			t.Fatalf("word %d = %d", i, w)
		}
	}
	if len(cc.transfers) != 2 {
		t.Errorf("transfers = %d, want 2", len(cc.transfers))
	}
	// A second drain reuses the buffer and continues the ramp (pop semantics).
	if !d.drainWords(burstPortPhys, dst, 10) || dst[0] != 3000 {
		t.Errorf("second drain: dst[0]=%d, want 3000", dst[0])
	}
}

// ---- CS1 cycle-to-cycle gap ----

type fakeGPMC struct {
	regs   map[uint32]uint32
	events []string
}

func (f *fakeGPMC) R(off uint32) uint32 { return f.regs[off] }
func (f *fakeGPMC) W(off, v uint32) {
	f.regs[off] = v
	switch off {
	case gpmcConfig6:
		f.events = append(f.events, "cfg6")
	case gpmcConfig7:
		if v&c7CSValid == 0 {
			f.events = append(f.events, "csvalid-off")
		} else {
			f.events = append(f.events, "csvalid-on")
		}
	}
}

func TestCS1CycleGapSequence(t *testing.T) {
	g := &fakeGPMC{regs: map[uint32]uint32{gpmcConfig6: 0x06000041, gpmcConfig7: 0x00000F41}}
	if err := applyCS1CycleGap(g, cs1CycleGap); err != nil {
		t.Fatal(err)
	}
	// On-device value confirmed after the write: CONFIG6_1 = 0x060005c1 (spec 12 §5.5).
	if got := g.regs[gpmcConfig6]; got != 0x060005c1 {
		t.Errorf("CONFIG6_1 = %#08x, want 0x060005c1", got)
	}
	if got := g.regs[gpmcConfig7]; got != 0x00000F41 {
		t.Errorf("CONFIG7_1 not restored exactly: %#08x", got)
	}
	want := []string{"csvalid-off", "cfg6", "csvalid-on"}
	if len(g.events) != len(want) {
		t.Fatalf("events %v, want %v", g.events, want)
	}
	for i := range want {
		if g.events[i] != want[i] {
			t.Fatalf("events %v, want %v", g.events, want)
		}
	}
	if err := applyCS1CycleGap(g, 16); err == nil {
		t.Error("gap 16 must be refused (4-bit field)")
	}
	// A gap already programmed differently is replaced, not OR-ed.
	g.regs[gpmcConfig6] = 0x06000F80 | 0x41
	if err := applyCS1CycleGap(g, 4); err != nil {
		t.Fatal(err)
	}
	if got := g.regs[gpmcConfig6]; got != 0x060004c1 {
		t.Errorf("CONFIG6_1 = %#08x, want 0x060004c1", got)
	}
}
