// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"open-sds/app/internal/analog"
	"open-sds/app/internal/decode"
	"open-sds/app/internal/engine"
	"open-sds/app/internal/panel/wsview"
)

// Each workspace's view, built every LCD tick on the device's single core.
// go test ./internal/panel -run - -bench WSView

// TRLC-LINKS: REQ-SDS-211
func BenchmarkWSView(b *testing.B) {
	for _, ws := range []struct {
		name string
		ws   wsview.WS
	}{{"scope", wsview.Scope}, {"bus", wsview.Bus}, {"spectrum", wsview.Spectrum}, {"measure", wsview.Measure}, {"compare", wsview.Compare}} {
		b.Run(ws.name, func(b *testing.B) {
			c, _, _ := newC(&testing.T{})
			c.switchWS(ws.ws)
			b.ReportAllocs()
			for b.Loop() {
				c.Snapshot()
			}
		})
	}
}

// TRLC-LINKS: REQ-SDS-212
func BenchmarkWrapText(b *testing.B) {
	s := "The chip or protocol the bus carries: the packet list, the strip and the web table name each transaction."
	for b.Loop() {
		wrapText(s, 38)
	}
}

// TRLC-LINKS: REQ-SDS-212
func BenchmarkRowValues(b *testing.B) {
	c, _, _ := newC(&testing.T{})
	s := c.snap()
	for b.Loop() {
		for _, t := range sheetTabs {
			for _, r := range t.rows {
				r.valueText(s)
				if r.kind == wsview.Number {
					r.gauge(s)
				}
			}
		}
	}
}

// TRLC-LINKS: REQ-SDS-136
func BenchmarkStepNs(b *testing.B) {
	for b.Loop() {
		for _, v := range []float64{10, 37, 1e3, 2e6, 9.9e9} {
			stepNs(v, 1, false)
			stepNs(v, -1, true)
		}
	}
}

// TRLC-LINKS: REQ-SDS-215
func BenchmarkCodesOf(b *testing.B) {
	const n = 20000
	f := &engine.Frame{C1: make([]uint8, n), C2: make([]uint8, n), Q1: make([]uint16, n), Valid: n}
	for i := range f.C1 {
		f.C1[i], f.Q1[i] = uint8(i), uint16(i*37)
	}
	for b.Loop() {
		codesOf(f, 0, 0, n, analog.CplDC) // Q8.8
		codesOf(f, 1, 0, n, analog.CplDC) // codes
	}
}

// TRLC-LINKS: REQ-SDS-214
func BenchmarkTokMatch(b *testing.B) {
	ps := make([]Packet, 64)
	for i := range ps {
		ps[i].Bytes = []byte{0x50, byte(i), 0x10, 0x20, byte(i * 7), 0x55, 0xAA, 0x0F}
	}
	toks := []string{"55", "A?"}
	for b.Loop() {
		for i := range ps {
			tokMatch(toks, ps[i])
		}
	}
}

// TRLC-LINKS: REQ-SDS-216
func BenchmarkSpecPane(b *testing.B) {
	c, _, _ := newC(&testing.T{})
	c.switchWS(wsview.Spectrum)
	c.amu.Lock()
	c.ana.specOK = true
	c.ana.spec.BinHz, c.ana.spec.SpanHz, c.ana.spec.N, c.ana.spec.EffNyq = 1220.7, 2.5e6, 4096, 2.5e6
	for i := 0; i < maxPeaks; i++ {
		c.ana.peaks = append(c.ana.peaks, Peak{Freq: 1e3 * float64(2*i+1), DB: -6 - 9.5*float64(i), RelDB: -9.5 * float64(i), Bin: 2*i + 1, Harm: 2*i + 1})
	}
	c.ana.floorDBV = -92
	c.amu.Unlock()
	s := c.snap()
	for b.Loop() {
		c.specPane(s)
	}
}

// TRLC-LINKS: REQ-SDS-213
func BenchmarkToolLine(b *testing.B) {
	c, _, _ := newC(&testing.T{})
	c.switchWS(wsview.Measure)
	s := c.snap()
	for b.Loop() {
		_ = append(toolLine(s, wsview.Seg{Key: "Adjust", Job: "cursor A"}, wsview.Seg{Key: "push", Job: "cursors"}), wsview.Seg{Key: "Zoom", Job: "x"})
	}
}

// TRLC-LINKS: REQ-SDS-213
func BenchmarkToolLineRef(b *testing.B) {
	c, _, _ := newC(&testing.T{})
	c.switchWS(wsview.Measure)
	s := c.snap()
	for b.Loop() {
		_ = append(append([]wsview.Seg{{Key: "Adjust", Job: "cursor A"}, {Key: "push", Job: "cursors"}}, posTool(s)...), wsview.Seg{Key: "Zoom", Job: "x"})
	}
}

// TRLC-LINKS: REQ-SDS-218
func BenchmarkStabKey(b *testing.B) {
	c, _, _ := newC(&testing.T{})
	s := c.snap()
	for b.Loop() {
		_ = stabKey(s)
	}
}

// TRLC-LINKS: REQ-SDS-136
func BenchmarkSetupPrint(b *testing.B) {
	c, _, _ := newC(&testing.T{})
	s := c.snap()
	for b.Loop() {
		setupPrint(s)
	}
}

// TRLC-LINKS: REQ-SDS-229
func BenchmarkSPIEyeKey(b *testing.B) {
	c, _, _ := newC(&testing.T{})
	s := c.snap()
	s.hasSerial, s.serial.SPIClockHz = true, 10000000
	k0 := spiEyeK{}
	for b.Loop() {
		if spiEyeKeyOf(s) == k0 {
			b.Fatal("unset key")
		}
	}
}

// TRLC-LINKS: REQ-SDS-229
func BenchmarkSPIEyeKeyRef(b *testing.B) {
	c, _, _ := newC(&testing.T{})
	s := c.snap()
	s.hasSerial, s.serial.SPIClockHz = true, 10000000
	for b.Loop() {
		key := fmt.Sprint(s.decChA, s.decChB, s.decCPOL, s.decCPHA)
		if s.hasSerial {
			key += fmt.Sprint(" ", s.serial.SPIClockHz)
		}
		if key == "" {
			b.Fatal("unset key")
		}
	}
}

// stateFE is a front end that also reads a channel's settings under one
// lock (analog.FrontEnd's ChannelState) and has bandwidth limits.
type stateFE struct {
	*fakeFE
	bwl [2]bool
}

// TRLC-LINKS: REQ-SDS-135
func (f *stateFE) BWL(ch int) bool { return f.bwl[ch&1] }

// TRLC-LINKS: REQ-SDS-135
func (f *stateFE) ChannelState(ch int) (offV float64, cpl int, probe float64, bwl bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.offReqV[ch], f.cpl[ch], f.probe[ch], f.bwl[ch&1]
}

// TRLC-LINKS: REQ-SDS-211
func stateC(t *testing.T) (*Controller, *stateFE) {
	fe := &stateFE{fakeFE: &fakeFE{idx: [2]int{8, 8}}, bwl: [2]bool{false, true}}
	c := New(newEng(), fe, -1, engine.SupportedTdivs(), 500e-6, t.Logf)
	c.decode(idle(), true)
	return c, fe
}

// A snapshot through ChannelState reads what the per-value calls read.
// TRLC-LINKS: REQ-SDS-211
func TestSnapChannelStateMatches(t *testing.T) {
	c, fe := stateC(t)
	fe.SetProbe(0, 10)
	fe.SetProbe(1, 0.5) // below 1: reads 1
	fe.SetCoupling(1, analog.CplAC)
	fe.offReqV = [2]float64{0.25, -1.5}
	s := c.snap()
	if s.probe != [2]float64{10, 1} || s.cpl != [2]int{analog.CplDC, analog.CplAC} || s.offV != fe.offReqV || !s.hasBWL || s.bwl != fe.bwl {
		t.Fatalf("snap read probe %v cpl %v offV %v bwl %v %v", s.probe, s.cpl, s.offV, s.hasBWL, s.bwl)
	}
}

// TRLC-LINKS: REQ-SDS-211
func BenchmarkSnapFE(b *testing.B) {
	c, _ := stateC(&testing.T{})
	for b.Loop() {
		c.snap()
	}
}

// TRLC-LINKS: REQ-SDS-273
func BenchmarkSoundPane(b *testing.B) {
	c, _, _ := newC(&testing.T{})
	ss := c.snd
	ss.mu.Lock()
	ss.hz = 440.8
	for i := 0; i < 80; i++ {
		ss.strip = append(ss.strip, stripPoint{cents: float64(i % 7), ok: i > 3})
	}
	ss.mu.Unlock()
	s := c.snap()
	for b.Loop() {
		c.soundPane(s)
	}
}

// TRLC-LINKS: REQ-SDS-214, REQ-SDS-222
func BenchmarkDecodesBus(b *testing.B) {
	var bad decode.Result // a 115200 Bd line read at 460800 Bd: one unit in five an error
	bad.OK = true
	for i := 0; i < 2000; i++ {
		kind := "data"
		if i%5 == 0 {
			kind = "frame-error"
		}
		bad.Spans = append(bad.Spans, decode.Span{I0: i, I1: i + 1, Text: "55", Kind: kind, Val: 0x55})
		bad.Bytes = append(bad.Bytes, 0x55)
	}
	for b.Loop() {
		if decodesBus(bad) {
			b.Fatal("decodes")
		}
	}
}

// TRLC-LINKS: REQ-SDS-212
func BenchmarkGuidePage(b *testing.B) {
	c, _, _ := newC(&testing.T{})
	s := c.snap()
	for b.Loop() {
		for ws := wsview.WS(0); ws < wsview.Count; ws++ {
			s.ui.ws = ws
			guidePage(s)
		}
	}
}

// TRLC-LINKS: REQ-SDS-221
func BenchmarkMinuteOpts(b *testing.B) {
	for b.Loop() {
		minuteOpts(saverMins)
		minuteOpts(offMins)
	}
}

// TRLC-LINKS: REQ-SDS-013
func BenchmarkTrigTypeRow(b *testing.B) {
	c, _, _ := newC(&testing.T{})
	s := c.snap()
	r := rowByID["trig.type"]
	if r == nil {
		b.Fatal("no trig.type row")
	}
	for b.Loop() {
		r.get(s)
	}
}

// TRLC-LINKS: REQ-SDS-273
func BenchmarkSoundRowInfo(b *testing.B) {
	c, _, _ := newC(&testing.T{})
	for b.Loop() {
		c.snd.rowInfo()
	}
}

// TRLC-LINKS: REQ-SDS-141
func BenchmarkSRRecordText(b *testing.B) {
	for b.Loop() {
		if srRecordText(123, 4096) != "FPGA: record 123/4096..." {
			b.Fatal("text")
		}
	}
}

// TRLC-LINKS: REQ-SDS-141
func BenchmarkSRRecordTextRef(b *testing.B) {
	for b.Loop() {
		if fmt.Sprintf("FPGA: record %d/%d...", 123, 4096) != "FPGA: record 123/4096..." {
			b.Fatal("text")
		}
	}
}

// TRLC-LINKS: REQ-SDS-216
func BenchmarkMarkerStatus(b *testing.B) {
	c, _, _ := newC(&testing.T{})
	s := c.snap()
	r := rowByID["view.marker"]
	for b.Loop() {
		for _, v := range []int{viewFFT, viewBode} {
			s.viewMode = v
			r.status(s)
		}
	}
}

// TRLC-LINKS: REQ-SDS-212
func BenchmarkRowFKeys(b *testing.B) {
	c, _, _ := newC(&testing.T{})
	s := c.snap()
	rs := []*row{rowByID["trig.type"], rowByID["trig.mode"], rowByID["trig.edge"]}
	for _, t := range sheetTabs {
		for _, r := range t.rows {
			if r.kind == wsview.Slot || r.kind == wsview.Number {
				rs = append(rs, r)
			}
		}
	}
	for b.Loop() {
		for _, r := range rs {
			c.rowKeys(s, r)
		}
	}
}

// TRLC-LINKS: REQ-SDS-212
func BenchmarkParseNumber(b *testing.B) {
	in := [][2]string{{"2.5MHz", "Hz"}, {"115200", ""}, {"10 mV", "V"}, {"-1.5e-3 V", "V"}, {"0x1F", ""}, {"2,5k", "Hz"}, {"40 ns", "s"}}
	for b.Loop() {
		for _, x := range in {
			parseNumber(x[0], x[1])
		}
	}
}

// parseNumber's results and errors on odd input, as the rune-slice reading
// gave them.
// TRLC-LINKS: REQ-SDS-212
func TestParseNumberOddInput(t *testing.T) {
	for _, c := range []struct {
		in, unit, want string
	}{
		{"5 \xffV", "V", `unit "\xffV" does not fit (want V)`},
		{"5 k\xffV", "V", `unit "�V" does not fit (want V)`},
		{"0XfF", "", "255"},
		{"-0x10", "", "-16"},
		{"2µs", "s", "2e-06"},
		{"3 MHz", "Hz", "3e+06"},
	} {
		v, err := parseNumber(c.in, c.unit)
		got := fmt.Sprint(v)
		if err != nil {
			got = err.Error()
		}
		if got != c.want {
			t.Errorf("parseNumber(%q, %q) = %s, want %s", c.in, c.unit, got, c.want)
		}
	}
}

// soundBlock is a sound block of n samples with a scale change in it.
// TRLC-LINKS: REQ-SDS-265
func soundBlock(n int) engine.SoundBlock {
	b := engine.SoundBlock{First: 1000, N: n, Q1: make([]uint16, n), Q2: make([]uint16, n),
		Scales: []engine.SoundScale{{From: 0, VdivV: [2]float64{0.1, 0.2}, OffV: [2]float64{0.01, -0.02}},
			{From: 1000 + uint64(n/3), VdivV: [2]float64{0.05, 0.2}, OffV: [2]float64{0, 0.5}},
			{From: 1000 + uint64(n/3), VdivV: [2]float64{0.5, 2}}, // two from one sample: the last stands
			{From: 1000 + uint64(2*n/3), VdivV: [2]float64{1, 1}}}}
	for i := range b.Q1 {
		b.Q1[i], b.Q2[i] = uint16(32768+i*97), uint16(i*13)
	}
	return b
}

// TRLC-LINKS: REQ-SDS-265
func BenchmarkSoundVolts(b *testing.B) {
	c, _, _ := newC(&testing.T{})
	blk := soundBlock(12000)
	var dst []float64
	for b.Loop() {
		dst = c.snd.volts(blk, 0, dst)
	}
}

// volts per scale segment reads as volts per sample did.
// TRLC-LINKS: REQ-SDS-265
func TestSoundVoltsSegments(t *testing.T) {
	c, _, _ := newC(t)
	ref := func(b engine.SoundBlock, ch int) []float64 {
		q := b.Q1
		if ch == 1 {
			q = b.Q2
		}
		var dst []float64
		k, vpc, off := -1, 1/256.0*256, 0.0
		for i, v := range q {
			ord := b.First + uint64(i)
			for k+1 < len(b.Scales) && (k < 0 || b.Scales[k+1].From <= ord) {
				k++
				vpc, off = b.Scales[k].SoundVolts(ch)
			}
			dst = append(dst, (float64(v)-32768)*vpc/256-off)
		}
		return dst
	}
	for _, n := range []int{0, 1, 2, 7, 300} {
		for _, cut := range []int{0, 1, 2, 3, 4} {
			b := soundBlock(n)
			b.Scales = b.Scales[:cut]
			for ch := 0; ch < 2; ch++ {
				got, want := c.snd.volts(b, ch, nil), ref(b, ch)
				if len(got) != len(want) {
					t.Fatalf("n %d scales %d: %d volts, want %d", n, cut, len(got), len(want))
				}
				for i := range got {
					if math.Float64bits(got[i]) != math.Float64bits(want[i]) {
						t.Fatalf("n %d scales %d ch %d [%d]: %v, want %v", n, cut, ch, i, got[i], want[i])
					}
				}
			}
		}
	}
}

// TRLC-LINKS: REQ-SDS-275
func BenchmarkTakePane(b *testing.B) {
	c, _, _ := newC(&testing.T{})
	ss := c.snd
	blk := soundBlock(48000)
	ss.mu.Lock()
	ss.tv.tk = &engine.SoundTake{SoundBlock: blk, ID: 3, Why: "stop", HasTrig: true, Trig: blk.First + 12000}
	ss.tv.tk.RateHz = 48000
	ss.tv.from, ss.tv.span = 2000, 30000
	ss.takePaneLocked(soundTakeWidth) // the columns, once
	ss.mu.Unlock()
	for b.Loop() {
		ss.mu.Lock()
		ss.takePaneLocked(soundTakeWidth)
		ss.mu.Unlock()
	}
}

// The take's title reads as its fmt form.
// TRLC-LINKS: REQ-SDS-275
func TestTakeTitle(t *testing.T) {
	c, _, _ := newC(t)
	ss := c.snd
	blk := soundBlock(48000)
	for _, why := range []string{"stop", "single", "normal", "other"} {
		for _, from := range []float64{0, 2000, 30000} {
			ss.mu.Lock()
			ss.tv.tk = &engine.SoundTake{SoundBlock: blk, ID: 3, Why: why, HasTrig: true, Trig: blk.First + 12000}
			ss.tv.tk.RateHz = 48000
			ss.tv.from, ss.tv.span = from, 9000
			p := ss.takePaneLocked(soundTakeWidth)
			ss.mu.Unlock()
			what := map[string]string{"stop": "STOP", "single": "SINGLE", "normal": "NORMAL"}[why]
			start := (from - 12000) / 48000
			want := fmt.Sprintf("%s take %.1f s  view %s at %+.3f s", what, 1.0, si(9000.0/48000, "s"), start)
			if p.Title != want {
				t.Errorf("title %q, want %q", p.Title, want)
			}
		}
	}
}

// TRLC-LINKS: REQ-SDS-013
func BenchmarkTokensOf(b *testing.B) {
	bytes := engine.SerialParams{Bytes: []int{0x55, 0xAA, 0x01, 0x80, 0xFF, 0x10, 0x20, 0x30}}
	pat := engine.SerialParams{Pattern: []engine.PatternElem{{Kind: 1}, {Kind: 2, Value: 0x5A, Mask: 0xff}, {Kind: 2, Value: 0xA0, Mask: 0xf0},
		{Kind: 2, Value: 0x0C, Mask: 0x0f}, {Kind: 2, Mask: 0}, {Kind: 4}}}
	for b.Loop() {
		tokensOf(bytes)
		tokensOf(pat)
	}
}

// Match tokens read as their fmt forms.
// TRLC-LINKS: REQ-SDS-013
func TestTokensOfHex(t *testing.T) {
	p := engine.SerialParams{Bytes: []int{0, 0x5, 0xAB, 0xFF, -1, 0x1FF}}
	if got := strings.Join(tokensOf(p), " "); got != "00 05 AB FF FF FF" {
		t.Errorf("bytes: %q", got)
	}
	p = engine.SerialParams{Pattern: []engine.PatternElem{{Kind: 2, Value: 0xA0, Mask: 0xf0}, {Kind: 2, Value: 0x0C, Mask: 0x0f}, {Kind: 2, Value: 0x3E, Mask: 0xff}}}
	if got := strings.Join(tokensOf(p), " "); got != "A? ?C 3E" {
		t.Errorf("pattern: %q", got)
	}
}

// TRLC-LINKS: REQ-SDS-138
func BenchmarkScreenColumns(b *testing.B) {
	sig := make([]uint8, 20000)
	for i := range sig {
		sig[i] = uint8(i * 7)
	}
	for b.Loop() {
		screenColumns(sig, 100, 12000, false)
		screenColumns(sig, -50, 400, true)
	}
}

// TRLC-LINKS: REQ-SDS-136
func BenchmarkSettingsView(b *testing.B) {
	c, _, _ := newC(&testing.T{})
	for b.Loop() {
		c.SettingsView()
	}
}

// TRLC-LINKS: REQ-SDS-136
func BenchmarkAdoptQual(b *testing.B) {
	c, _, _ := newC(&testing.T{})
	st := c.eng.Snapshot()
	st.TrigQual.PulseLvl = 0.5
	for b.Loop() {
		c.adoptQual(&st)
	}
}

// TRLC-LINKS: REQ-SDS-136
func BenchmarkAdoptQualRef(b *testing.B) {
	c, _, _ := newC(&testing.T{})
	st := c.eng.Snapshot()
	st.TrigQual.PulseLvl = 0.5
	byValue := func(st engine.Stats) { c.adoptQual(&st) } // the struct passed by value, as before
	for b.Loop() {
		byValue(st)
	}
}

// TRLC-LINKS: REQ-SDS-212, REQ-SDS-213
func BenchmarkStepTrigLevel(b *testing.B) {
	c, _, _ := newC(&testing.T{})
	n := 1
	for b.Loop() {
		c.stepTrigLevel(n)
		n = -n
	}
}
