// ENGMODEL-OWNER-UNIT: FU-APP-LCD
package lcd

import (
	"math/rand"
	"strings"

	"open-sds/app/internal/panel/wsview"
)

// fuzzView is a hostile workspace UI for the render fuzz: any workspace,
// pane, sheet or picker, with out-of-range layout, indices, cursors and
// grids.
// TRLC-LINKS: REQ-SDS-211, REQ-SDS-212
func fuzzView(rng *rand.Rand) wsview.View {
	pick := func(n int) bool { return rng.Intn(n) == 0 }
	str := func() string {
		return []string{"", "x", "AUTO: UART 115200 8N1 on C1, decode on", strings.Repeat("W", 300), "\x00\xff"}[rng.Intn(5)]
	}
	spans := func() []wsview.Span {
		out := make([]wsview.Span, rng.Intn(30))
		for i := range out {
			out[i] = wsview.Span{Text: str(), Col: wsview.Col(rng.Intn(20))}
		}
		return out
	}
	pane := wsview.Pane{Title: str(), Caption: str(), CapCol: wsview.Col(rng.Intn(20)), Right: str(), Again: str(),
		Tool: []wsview.Seg{{Key: str(), Job: str()}, {Key: "Adjust", Job: "packet"}}}
	v := wsview.View{WS: wsview.WS(rng.Intn(9) - 2), Rail: pick(2), Notice: str(),
		Layout: wsview.Layout{MenuW: []int{-50, 0, 30, 116, 2000}[rng.Intn(5)], PlotTop: []int{-9, 0, 104, 150, 900}[rng.Intn(5)],
			HideTraces: pick(2), HideBottom: pick(2), HideMeas: pick(2), HideViewCaptions: pick(2), Sheet: pick(3)}}
	if pick(2) {
		v.RailNote = &wsview.Note{WS: wsview.WS(rng.Intn(9) - 2), Head: str(), Text: str()}
	}
	if pick(2) {
		k := &wsview.Keys{Title: str(), Top: rng.Intn(600) - 50}
		for i := range k.Items {
			k.Items[i] = wsview.Key{Label: str(), Value: str(), Sel: pick(2), Dim: pick(2)}
		}
		v.Keys = k
	}
	toks := make([]wsview.PickTok, rng.Intn(40))
	for i := range toks {
		toks[i] = wsview.PickTok{Text: []string{"55", "ERR", ""}[rng.Intn(3)], Start: pick(3)}
	}
	switch rng.Intn(9) {
	case 0:
		v.Scope = &wsview.ScopePane{AdjustTag: str(), Hot: pick(2)}
	case 1:
		b := &wsview.BusPane{Pane: pane, Off: pick(3), Msg: spans(), Error: str(), Empty: str(), Up: pick(2), Dn: pick(2)}
		for i := rng.Intn(20); i > 0; i-- {
			ln := wsview.BusLine{Num: rng.Intn(5) - 1, Sel: pick(2), Hex: make([]string, rng.Intn(40)), Err: make([]bool, rng.Intn(40)),
				Str: strings.Repeat("A", rng.Intn(40)), More: pick(2), Bytes: rng.Intn(99), Match: str(), Cut: str()}
			b.Lines = append(b.Lines, ln)
		}
		b.Marks = []wsview.PacketMark{{I0: rng.Intn(9999) - 99, I1: rng.Intn(9999) - 99, Sel: pick(2), Match: pick(2)}}
		v.Bus = b
	case 2:
		v.Picker = &wsview.Picker{Caption: str(), Toks: toks, Cur: rng.Intn(50) - 5, Lines: spans(), Now: str()}
	case 3:
		e := &wsview.EyePane{Pane: pane, Why: []string{"", "Folding"}[rng.Intn(2)], Cursor: str(), Warn: str(), Note: str(),
			Grid: make([]uint32, rng.Intn(80000)), Cols: rng.Intn(500) - 10, Rows: rng.Intn(300) - 10, Max: uint32(rng.Intn(3)),
			Ch: rng.Intn(4) - 1, HasBox: pick(2), SampleCol: rng.Intn(900) - 100, ThreshRow: rng.Intn(900) - 100,
			Box: [4]int{rng.Intn(900) - 100, rng.Intn(900) - 100, rng.Intn(900) - 100, rng.Intn(900) - 100}}
		for i := range e.Grid {
			e.Grid[i] = uint32(rng.Intn(4))
		}
		v.Eye = e
	case 4:
		s := &wsview.SpecPane{Pane: pane, Bode: pick(2), Empty: str(), Readout: spans(), Marker: str(), Ch: rng.Intn(4) - 1,
			HasMark: pick(2), MarkHz: rng.Float64()*2e9 - 1e8, MarkRel: rng.Float64()*400 - 200, MarkText: str(), HasRef: pick(2),
			RefHz: rng.Float64() * 1e9, BodeIdx: rng.Intn(20) - 5, BodeRef: rng.Intn(20) - 5}
		for i := rng.Intn(30); i > 0; i-- {
			s.Rows = append(s.Rows, wsview.SpecRow{Num: i, Freq: str(), Level: str(), Rel: str(), Harm: str(), Sel: pick(2)})
		}
		v.Spec = s
	case 5:
		m := &wsview.MeasPane{Pane: pane, All: pick(2), Empty: pick(3), More: str(), AllN: rng.Intn(40) - 5, Cursor: spans(),
			MaskLine: wsview.Span{Text: str()}}
		for i := rng.Intn(30); i > 0; i-- {
			m.Stats = append(m.Stats, wsview.StatRow{Name: str(), Now: str(), Mean: str(), N: str()})
		}
		v.Meas = m
	case 6:
		c := &wsview.CmpPane{Pane: pane}
		for i := range c.Rows {
			c.Rows[i] = wsview.CmpRow{Name: str(), Info: str(), Extra: str(), Sel: pick(2), Action: str(), Bar: pick(2),
				BarN: rng.Intn(200) - 50, BarOf: rng.Intn(100) - 10, BarNote: str()}
		}
		v.Cmp = c
	default:
		sh := &wsview.Sheet{WSName: str(), Status: spans(), Guide: spans(), Hint: wsview.Span{Text: str()},
			ThumbView: rng.Intn(8) - 1, FFTCh: rng.Intn(4) - 1,
			Side: wsview.Side{Title: str(), Label: str(), Value: str(), Big: pick(2), Lines: spans(), Hints: spans()}}
		for i := rng.Intn(20); i > 0; i-- {
			sh.Tabs = append(sh.Tabs, wsview.Tab{Name: str(), Own: pick(2), Active: pick(3)})
		}
		for i := rng.Intn(40); i > 0; i-- {
			r := wsview.Row{Kind: wsview.RowKind(rng.Intn(8)), Header: str(), Label: str(), Rel: pick(2), Focus: pick(4), Edit: pick(4),
				Value: str(), Gauge: rng.Float64()*3 - 1, Status: str(), Armed: pick(2), Used: pick(2), Meas: [2]string{str(), str()}}
			for j := rng.Intn(15); j > 0; j-- {
				r.Opts = append(r.Opts, wsview.Opt{Text: str(), Sel: pick(4), Quiet: pick(2)})
				r.Acts = append(r.Acts, wsview.Act{Text: str(), Dim: pick(2), Flash: pick(3)})
			}
			sh.Rows = append(sh.Rows, r)
		}
		if pick(2) {
			sh.Sub = &wsview.Sub{Title: str(), Toks: toks, Cur: rng.Intn(50) - 5, Lines: spans()}
		}
		v.Sheet = sh
	}
	return v
}
