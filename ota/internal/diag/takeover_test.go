// ENGMODEL-OWNER-UNIT: FU-OTA-DIAG

package diag

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// fakeBoard is the probe image on a board: each ball open, held at a level,
// a clock, or a converter lane, whose core's code follows its channel's
// offset DAC while its leg runs and holds while it does not.
type fakeBoard struct {
	balls   []Ball
	pull    bool
	absent  map[string]bool // converter cores not fitted
	noFront map[int]bool    // channels (0, 1) whose offset DAC moves nothing
	held    map[string]bool // balls driven at a level: true high
	clocks  map[string]uint32
	legs    uint16
	dac     [2]uint16
	dacLo   [2]uint16
	code    map[string]uint8 // each core's last code
	chsel   [8]uint16
	e0, e1  []bool
	edges   [8]uint32
	cfgOut  []uint16 // configuration port writes
}

// newFakeBoard is the SDS1102CML+ as the probe sees it: the balls its
// reference names low held low, the clock inputs it names high held high
// (they take no pull-up), the others open.
// TRLC-LINKS: REQ-SDS-288
func newFakeBoard(pull bool) *fakeBoard {
	f := &fakeBoard{
		balls: probeBalls(), pull: pull, absent: map[string]bool{}, noFront: map[int]bool{}, held: map[string]bool{},
		clocks: map[string]uint32{"M2": 100000}, code: map[string]uint8{},
	}
	for _, b := range f.balls {
		switch c := probeReference()[b.Ball]; {
		case c == "low":
			f.held[b.Ball] = false
		case c == "high" && b.Group == "clkin":
			f.held[b.Ball] = true
		}
	}
	return f
}

// lane is the core and bit of a converter lane ball.
// TRLC-LINKS: REQ-SDS-288
func lane(b Ball) (core string, bit int, ok bool) {
	if b.Group != "adc" {
		return "", 0, false
	}
	i := strings.LastIndex(b.Name, ".b")
	return b.Name[:i], int(b.Name[i+2] - '0'), true
}

// coreCode is a core's code at its channel's DAC: the rails at the DAC's
// ends, mid-scale (0x7f/0x80, every bit flipping) between.
// TRLC-LINKS: REQ-SDS-288
func (f *fakeBoard) coreCode(core string, alt bool) uint8 {
	ch := int(core[len(core)-1] - '1')
	d := f.dac[ch]
	switch {
	case f.noFront[ch]:
		d = 0x8000
	case d < 0x2000:
		return 0x00
	case d > 0xe000:
		return 0xff
	}
	_ = d
	if alt {
		return 0x80
	}
	return 0x7f
}

// level is ball i's level at one instant of a gate.
// TRLC-LINKS: REQ-SDS-288
func (f *fakeBoard) level(i int, alt bool) (v, toggles bool) {
	b := f.balls[i]
	if hz, ok := f.clocks[b.Ball]; ok {
		return alt, hz > 0
	}
	if lv, ok := f.held[b.Ball]; ok {
		return lv, false
	}
	if core, bit, ok := lane(b); ok && !f.absent[core] {
		leg := -1
		for l, c := range legCore {
			if c == core {
				leg = l
			}
		}
		if f.legs>>leg&1 != 0 {
			f.code[core] = f.coreCode(core, alt)
		}
		return f.code[core]>>bit&1 != 0, false
	}
	return f.pull && b.Group != "clkin", false // open: the pull-up, or low
}

// TRLC-LINKS: REQ-SDS-288
func (f *fakeBoard) Read(plane uint8, sel uint16) (uint16, error) {
	if plane == planeCS3 {
		return cfgNSTATUS | cfgCONFDONE, nil
	}
	n := len(f.balls)
	word := func(fl []bool, w int) uint16 {
		var v uint16
		for i := 0; i < 16 && w*16+i < n; i++ {
			if fl != nil && fl[w*16+i] {
				v |= 1 << i
			}
		}
		return v
	}
	switch {
	case sel == prID:
		return probeID, nil
	case sel == prInfo:
		v := uint16(n) << 8
		if f.pull {
			v |= 1
		}
		return v, nil
	case sel == prStatus:
		return 2, nil
	case sel >= prEdges && sel < prEdges+16:
		c := (sel - prEdges) / 2
		if sel%2 == 1 {
			return uint16(f.edges[c] >> 16), nil
		}
		return uint16(f.edges[c]), nil
	case sel >= prHighs && sel < prHighs+16:
		return 0, nil
	case sel >= prEver0 && sel < prEver0+16:
		return word(f.e0, int(sel-prEver0)), nil
	case sel >= prEver1 && sel < prEver1+16:
		return word(f.e1, int(sel-prEver1)), nil
	case sel >= prLive && sel < prLive+16:
		live := make([]bool, n)
		for i := range live {
			live[i], _ = f.level(i, false)
		}
		return word(live, int(sel-prLive)), nil
	}
	return 0, nil
}

// TRLC-LINKS: REQ-SDS-288
func (f *fakeBoard) Write(plane uint8, sel, val uint16) error {
	if plane == planeCS3 {
		switch sel {
		case cs3Config:
			f.cfgOut = append(f.cfgOut, val)
		case offsetDAC[0][0], offsetDAC[1][0]:
			f.dacLo[sel-offsetDAC[0][0]] = val
		case offsetDAC[0][1], offsetDAC[1][1]:
			ch := sel - offsetDAC[0][1]
			f.dac[ch] = val<<8 | f.dacLo[ch]
		}
		return nil
	}
	switch {
	case sel == prLegs:
		f.legs = val
	case sel >= prChan && sel < prChan+8:
		f.chsel[sel-prChan] = val
	case sel == prStart:
		n := len(f.balls)
		f.e0, f.e1 = make([]bool, n), make([]bool, n)
		for _, alt := range []bool{false, true} {
			for i := range n {
				v, _ := f.level(i, alt)
				f.e0[i] = f.e0[i] || !v
				f.e1[i] = f.e1[i] || v
			}
		}
		for c, s := range f.chsel {
			f.edges[c] = 0
			if int(s) < n {
				if _, t := f.level(int(s), false); t {
					f.edges[c] = f.clocks[f.balls[s].Ball]
				}
			}
		}
	}
	return nil
}

// TestProbeBallsMatchTheImage keeps the census list equal to the FPGA's.
// TRLC-LINKS: REQ-SDS-288
func TestProbeBallsMatchTheImage(t *testing.T) {
	b, err := os.ReadFile("../../../fpga/images/probe/balls.txt")
	if err != nil {
		t.Skip("no fpga tree:", err)
	}
	if string(b) != probeBallsText {
		t.Fatal("ota/internal/diag/probe_balls.txt differs from fpga/images/probe/balls.txt")
	}
	bs := probeBalls()
	if len(bs) != 140 || bs[len(bs)-1].Ball != "M2" {
		t.Fatalf("%d balls, last %v", len(bs), bs[len(bs)-1])
	}
	ref := probeReference()
	for _, b := range bs {
		if _, ok := ref[b.Ball]; ok == (b.Group == "adc") {
			t.Errorf("ball %s %s: in the reference %v", b.Ball, b.Name, ok)
		}
	}
	cores := map[string]int{}
	for _, b := range bs {
		if c, _, ok := lane(b); ok {
			cores[c]++
		}
	}
	for _, c := range legCore {
		if cores[c] != 8 {
			t.Fatalf("core %s has %d lanes", c, cores[c])
		}
	}
}

// censusPair is the census of both builds on boards set up by setup.
// TRLC-LINKS: REQ-SDS-288
func censusPair(setup func(*fakeBoard)) (Census, Census) {
	nap := func(time.Duration) {}
	var out [2]Census
	for i, pull := range []bool{false, true} {
		f := newFakeBoard(pull)
		setup(f)
		out[i] = runCensus(f, len(f.balls), nap)
	}
	return out[0], out[1]
}

// verdictOf is the verdict of part.
// TRLC-LINKS: REQ-SDS-288
func verdictOf(t *testing.T, vs []Verdict, part string) Verdict {
	t.Helper()
	for _, v := range vs {
		if v.Part == part {
			return v
		}
	}
	t.Fatalf("no verdict for %s in %v", part, vs)
	return Verdict{}
}

// TestCensusOfTheFullBoard: every converter, both front ends and the
// reference clock of the SDS1102CML+ are present.
// TRLC-LINKS: REQ-SDS-288
func TestCensusOfTheFullBoard(t *testing.T) {
	open, pull := censusPair(func(*fakeBoard) {})
	if open.Error != "" || pull.Error != "" {
		t.Fatal(open.Error, pull.Error)
	}
	vs := verdicts(probeBalls(), open, pull)
	for _, v := range vs {
		if strings.HasPrefix(v.Part, "converter") || strings.HasPrefix(v.Part, "channel") || v.Part == "reference clock (M2)" || v.Part == "encode PLL" {
			if v.Verdict != vPresent {
				t.Errorf("%s: %s %s", v.Part, v.Verdict, v.Detail)
			}
		}
	}
	for _, part := range []string{
		"SRAM bus (the app run says whether it works)", "front panel lines", "other balls",
		"balls unused on the SDS1102CML+", "clock inputs unused on the SDS1102CML+",
	} {
		if v := verdictOf(t, vs, part); v.Verdict != vPresent {
			t.Errorf("%s: %s %s", part, v.Verdict, v.Detail)
		}
	}
	if v := verdictOf(t, vs, "converter E1.CH1"); strings.Contains(v.Detail, "M2") {
		t.Errorf("the reference clock counted as a lane of a leg: %s", v.Detail)
	}
	if txt := verdictText(vs, probeBalls(), open, pull); !strings.Contains(txt, "G11  E1.CH1.b0") {
		t.Errorf("the report lists no balls:\n%s", txt)
	}
}

// TestCensusFindsWhatIsMissing: a board without converter pair E3, without
// channel 2's front end, and with something on a ball the SDS1102CML+
// leaves open.
// TRLC-LINKS: REQ-SDS-288
func TestCensusFindsWhatIsMissing(t *testing.T) {
	open, pull := censusPair(func(f *fakeBoard) {
		f.absent["E3.CH1"], f.absent["E3.CH2"] = true, true
		f.noFront[1] = true
		f.held["J12"] = false
	})
	vs := verdicts(probeBalls(), open, pull)
	for part, want := range map[string]string{
		"converter E3.CH1": vAbsent, "converter E3.CH2": vAbsent, "converter E1.CH1": vPresent, "converter E5.CH2": vPresent,
		"channel 1 front end and offset DAC": vPresent, "channel 2 front end and offset DAC": vAbsent,
		"balls unused on the SDS1102CML+": vOther,
	} {
		if v := verdictOf(t, vs, part); v.Verdict != want {
			t.Errorf("%s: %s (%s), want %s", part, v.Verdict, v.Detail, want)
		}
	}
	if v := verdictOf(t, vs, "converter E3.CH1"); !strings.Contains(v.Detail, "with the pull-up they read 11111111") {
		t.Errorf("an absent converter's open lanes: %s", v.Detail)
	}
	if v := verdictOf(t, vs, "balls unused on the SDS1102CML+"); !strings.Contains(v.Detail, "J12 unused.J12 low (high on the SDS1102CML+)") {
		t.Errorf("the driven unused ball: %s", v.Detail)
	}
}

// TestCensusNeedsTheProbeImage: another image answering is no census.
// TRLC-LINKS: REQ-SDS-288
func TestCensusNeedsTheProbeImage(t *testing.T) {
	cs := runCensus(regFunc(func(uint8, uint16) (uint16, error) { return 0x0052, nil }), 140, func(time.Duration) {})
	if !strings.Contains(cs.Error, "does not answer") {
		t.Fatal(cs.Error)
	}
	vs := verdicts(probeBalls(), cs, cs)
	if len(vs) != 1 || vs[0].Verdict != vAbsent {
		t.Fatal(vs)
	}
}

// regFunc is a read-only regBus.
type regFunc func(uint8, uint16) (uint16, error)

// TRLC-LINKS: REQ-SDS-288
func (r regFunc) Read(p uint8, s uint16) (uint16, error) { return r(p, s) }

// TRLC-LINKS: REQ-SDS-288
func (r regFunc) Write(uint8, uint16, uint16) error { return errors.New("read only") }

// TestConfigureShiftsLSBFirst: nCONFIG low then high, each byte's bits LSB
// first on DATA0 with a DCLK pulse, 128 clocks after, and CONF_DONE waited.
// TRLC-LINKS: REQ-SDS-288
func TestConfigureShiftsLSBFirst(t *testing.T) {
	f := newFakeBoard(false)
	rbf := make([]byte, 1024)
	rbf[0] = 0x01
	rbf[1] = 0x80
	if err := configure(f, rbf, func(time.Duration) {}); err != nil {
		t.Fatal(err)
	}
	w := f.cfgOut
	if w[0] != 0 || w[1] != cfgNCONFIG {
		t.Fatalf("start %x", w[:2])
	}
	data := w[2:]
	bit := func(n int) bool { return data[2*n]&cfgDATA0 != 0 }
	if !bit(0) || bit(1) || bit(7) || !bit(15) || bit(8) {
		t.Fatalf("bits of 0x01 0x80 not LSB first: % x", data[:32])
	}
	for n := range 16 {
		if data[2*n]&cfgDCLK != 0 || data[2*n+1] != data[2*n]|cfgDCLK || data[2*n]&cfgNCONFIG == 0 {
			t.Fatalf("clock pulse %d: %x %x", n, data[2*n], data[2*n+1])
		}
	}
	if got, want := len(w), 2+1024*16+128*2+1; got != want {
		t.Fatalf("%d writes, want %d", got, want)
	}
}

// TestTakeoverGate: the gate passes only with the poke done and the factory
// app running on, the configuration port configured, an EP4CE10 factory
// image and the stick's files.
// TRLC-LINKS: REQ-SDS-288
func TestTakeoverGate(t *testing.T) {
	good := takeoverInput{
		sweepDone: true, alive: true, cfg: 0x00c2, factoryRBF: []int64{factoryRBFLen},
		files: map[string]bool{probeRBF: true, probePullRBF: true, stickApp: true},
	}
	if ok, why := passed(TakeoverGate(good)); !ok {
		t.Fatal(why)
	}
	for name, in := range map[string]takeoverInput{
		"active stage":       {alive: true, cfg: 0xc0, factoryRBF: good.factoryRBF, files: good.files, sweepDone: false},
		"factory app after":  {sweepDone: true, cfg: 0xc0, factoryRBF: good.factoryRBF, files: good.files},
		"configuration port": {sweepDone: true, alive: true, cfg: 0x40, factoryRBF: good.factoryRBF, files: good.files},
		"fpga part":          {sweepDone: true, alive: true, cfg: 0xc0, factoryRBF: []int64{580000}, files: good.files},
		"stick files":        {sweepDone: true, alive: true, cfg: 0xc0, factoryRBF: good.factoryRBF, files: map[string]bool{probeRBF: true}},
	} {
		ok, why := passed(TakeoverGate(in))
		if ok || !strings.HasPrefix(why, name+":") {
			t.Errorf("%s: passed=%v %s", name, ok, why)
		}
	}
}

// TestKinIsTheParentChain: this test's parent is kin, a missing process is
// not.
// TRLC-LINKS: REQ-SDS-288
func TestKinIsTheParentChain(t *testing.T) {
	self := os.Getpid()
	if !kin(self, os.Getppid()) {
		t.Fatal("the parent is not kin")
	}
	if kin(self, 0x7ffffff0) {
		t.Fatal("a missing process is kin")
	}
}

// TestCensusOfTheSDS1102CMLPlus: the censuses the takeover stage took on an
// SDS1102CML+ booted cold under the factory firmware, in two sessions (the
// second after the full sweep), judge every part present.
// TRLC-LINKS: REQ-SDS-288
func TestCensusOfTheSDS1102CMLPlus(t *testing.T) {
	for _, run := range []string{"", "2-"} {
		var cs [2]Census
		for i, b := range []string{"probe", "probepull"} {
			data, err := os.ReadFile("testdata/census-sds1102cml-" + run + b + ".json")
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(data, &cs[i]); err != nil {
				t.Fatal(err)
			}
		}
		if cs[0].Pull || !cs[1].Pull {
			t.Fatal("the builds' pull-up flags")
		}
		for _, v := range verdicts(probeBalls(), cs[0], cs[1]) {
			if v.Verdict != vPresent {
				t.Errorf("session %q: %s: %s %s", run, v.Part, v.Verdict, v.Detail)
			}
		}
	}
}
