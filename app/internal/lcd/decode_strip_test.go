package lcd

import (
	"fmt"
	"strings"
	"testing"

	"open-sds/app/internal/analysis"
	"open-sds/app/internal/engine"
	"open-sds/app/internal/panel/framecalc"
	"open-sds/app/internal/panel/wsview"
)

// TestDecodeStripClearsTrace: with a protocol decoded, the Y-T plot ends
// above the decode strip in every workspace, so a trace low on the screen
// (here 3.5 divisions under the centre, where the strip covered it) is drawn
// above the decoded bytes, and the -4 division row sits just over the strip.
// TRLC-LINKS: REQ-SDS-021, REQ-SDS-214
func TestDecodeStripClearsTrace(t *testing.T) {
	const n = 1024
	f := &engine.Frame{C1: make([]uint8, n), C2: make([]uint8, n), Valid: n, WinCols: n, EdgeX: n / 2,
		Ptp: 0, TdivS: 100e-6, SampleS: 1e-6, Trigd: true}
	for i := range f.C1 {
		f.C1[i] = 128 - 88 // -3.52 div
	}
	for _, top := range []int{0, 104, 136, 142} {
		h := defaultHUD()
		h.TdivS, h.SampleS, h.TrigPosFrac = 100e-6, 1e-6, 0.5
		h.DecProto, h.DecBaud = 2, 9600
		h.ShowC2, h.ShowC1, h.ChanExplicit = false, true, true
		if top > 0 {
			h.UI.Layout = wsview.Layout{MenuW: 30, PlotTop: top}
		}
		p := plotOf(h.UI.Layout).aboveStrip()
		if p.sy(28) >= decodeStripTop || p.last() >= decodeStripTop {
			t.Fatalf("plot top %d: -4 div at row %d, plot ends %d: in the strip from %d", top, p.sy(28), p.last(), decodeStripTop)
		}
		sf := NewMemSurface()
		Render(sf, f, h, true)
		x, found := 300, -1
		for y := 12; y < decodeStripTop; y++ {
			if sf.At(x, y) == colC1 {
				found = y
			}
		}
		if found < 0 {
			t.Errorf("plot top %d: the trace at -3.5 div is not above the decode strip (column %d)", top, x)
		}
		h.DecProto = 0
		if q := plotOf(h.UI.Layout); decodeStrip(h) || q.last() != H-1 {
			t.Errorf("plot top %d: no decode, yet the plot ends at %d", top, q.last())
		}
	}
}

// lateSPIFlash is a record of a 104 MHz SPI flash's bursts of msg, mode 0
// MSB first, its data launched lateS after the falling clock edge (a fast
// device's late data: past the next rising edge), the clock on C2 and the
// data on C1, sampled every 2 ns; the record starts startS into the first
// burst and its bursts are gapS apart.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-214
func lateSPIFlash(msg []byte, lateS, startS, gapS float64, n int) (c1, c2 []uint8) {
	const period = 1 / 104e6
	type tr struct {
		t  float64
		up bool
	}
	var clk, dat []tr
	cl, dl := false, false
	set := func(list *[]tr, lvl *bool, at float64, v bool) {
		if *lvl != v {
			*list = append(*list, tr{at, v})
			*lvl = v
		}
	}
	for t := -startS; t < float64(n)*2e-9; t += float64(8*len(msg))*period + gapS {
		for i := 0; i < 8*len(msg); i++ {
			start := t + float64(i)*period
			set(&dat, &dl, start+lateS, msg[i/8]>>(7-i%8)&1 == 1)
			set(&clk, &cl, start+period/2, true)
			set(&clk, &cl, start+period, false)
		}
	}
	render := func(list []tr) []uint8 {
		out := make([]uint8, n)
		lvl, k := false, 0
		for i := range out {
			at := float64(i) * 2e-9
			for k < len(list) && list[k].t <= at {
				lvl = list[k].up
				k++
			}
			v := 70.0
			if lvl {
				v = 170
			}
			if k > 0 && at-list[k-1].t < 1.5e-9 { // a 1.5 ns ramp after each transition
				f := (at - list[k-1].t) / 1.5e-9
				if lvl {
					v = 70 + 100*f
				} else {
					v = 170 - 100*f
				}
			}
			out[i] = uint8(v)
		}
		return out
	}
	return render(dat), render(clk)
}

// TestDecodeStripReadsTheListsBytes: a 104 MHz SPI flash's late data, at
// 100 ns/div, on a screen of a tenth of the record that starts in the middle
// of a burst. The strip shows the bytes the packet list holds for the
// samples on screen (and the bytes sent): it decodes the record with the
// panel's settings, SPI's data delay among them; without it the strip read
// each bit one clock late ("80 08 00 7F A9" for "0B 00 10 00 FF").
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-214
func TestDecodeStripReadsTheListsBytes(t *testing.T) {
	msg := append([]byte{0x0B, 0x00, 0x10, 0x00, 0xFF}, "SPI FLASH 104MHZ OPEN-SDS 0123  "...)
	const n, win, delayNs = 5000, 500, 7.5
	c1, c2 := lateSPIFlash(msg, 7e-9, 1.5e-6, 2e-6, n)
	f := &engine.Frame{C1: c1, C2: c2, Valid: n, WinCols: win, EdgeX: 2600, SampleS: 2e-9, TdivS: 100e-9, Trigd: true, Seq: 7}
	h := defaultHUD()
	h.DecProto, h.DecChA, h.DecChB, h.DecDelayNs = 4, 1, 0, delayNs
	strip := decodeFrame(f, h)
	// the packet list: the panel's decode of the record, grouped
	list := analysis.Packets(framecalc.Decode(f, framecalc.Params(framecalc.ProtoSPI, 0, 1, 0, false, false, false, 0, delayNs)), n)
	if !strip.OK || len(list) == 0 {
		t.Fatalf("strip %v %q, %d packets", strip.OK, strip.Error, len(list))
	}
	var whole *analysis.Packet
	for i := range list {
		if list[i].Complete {
			whole = &list[i]
			break
		}
	}
	if whole == nil || string(whole.Bytes) != string(msg) {
		t.Fatalf("the list holds no complete packet of the message: %+v", list)
	}
	left, right := 2600-win/2, 2600+win/2 // the screen: the trigger at the middle
	// the strip's text on screen, and the complete packet's bytes there
	var shown, listed []string
	var sent []byte
	k := 0
	for _, s := range strip.Spans {
		if s.Kind != "data" {
			continue
		}
		in := s.I0 >= left && s.I1 < right
		if in {
			shown = append(shown, s.Text)
		}
		if s.I0 >= whole.I0 && s.I1 <= whole.I1 {
			if in {
				listed = append(listed, fmt.Sprintf("%02X", whole.Bytes[k]))
				sent = append(sent, whole.Bytes[k])
			}
			k++
		}
	}
	got, want := strings.Join(shown, " "), strings.Join(listed, " ")
	if len(shown) < 8 || got != want || !strings.Contains(string(msg), string(sent)) {
		t.Fatalf("strip on screen %s, the list there %s (sent % X)", got, want, msg)
	}
}
