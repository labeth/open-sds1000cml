// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"fmt"
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
