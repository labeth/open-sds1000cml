// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"fmt"
	"math"
	"time"

	"open-sds/app/internal/engine"
	"open-sds/app/internal/panel/framecalc"
	"open-sds/app/internal/panel/wsview"
)

// SPECTRUM (REQ-SDS-216): the FFT large, a small time trace, and a peak
// table with frequency, level, level against the top peak, harmonic number
// and THD. Adjust moves the marker peak to peak, Position bin by bin, the
// Position push back to the largest peak, the Adjust push sets a delta
// reference. SPECTRUM again cycles FFT, spectrogram and Bode.

// maxPeaks is the peak table's length.
const maxPeaks = 8

// specFrame computes the FFT source's spectrum in dBV and its peaks.
// TRLC-LINKS: REQ-SDS-216
func (c *Controller) specFrame(a *anaState, s *sv, f *engine.Frame) {
	if s.viewMode == viewFFT || s.viewMode == viewSpectrogram {
		// the frames a spectrum can be taken of, as the web's FFT asks for: the
		// full-rate record on short screens, else the filtered decimated one,
		// not the live envelope's extremes
		if e, ok := c.eng.(interface{ LeaseSpectrumView(time.Duration) }); ok {
			e.LeaseSpectrumView(3 * time.Second)
			a.specLease = true
		}
	}
	ch := s.fftSrc & 1
	sp, ok := framecalc.Spectrum(f, ch, f.SampleS)
	a.specOK, a.specCh = ok, ch
	if !ok || sp.BinHz <= 0 {
		a.specOK = false
		return
	}
	vpc := voltsPerCode(s, ch)
	db := make([]float64, len(sp.Mags))
	for k, m := range sp.Mags {
		// a sine of amplitude A codes reads A*N/4 through the Hann window
		db[k] = 20 * math.Log10(4*m/float64(sp.N)*vpc/math.Sqrt2+1e-15)
	}
	a.spec, a.specDBV = sp, db
	// The strongest peaks in frequency order: the table holds still frame to
	// frame where a ranking by level would jitter.
	a.peaks = anaPeaks(db, sp.BinHz, maxPeaks)
	a.thd = anaHarmonics(a.peaks, 0) // against the largest peak
	if len(db) > 2 {
		a.floorDBV = median(db[1:])
	}
}

// setBodeChannel sets the Bode reference or DUT channel in the engine,
// keeping its armed state; the other row's channel swaps over.
// TRLC-LINKS: REQ-SDS-066
func (c *Controller) setBodeChannel(dut bool, ch int) error {
	b, ok := c.eng.(interface{ SetBodeMode(bool, int, int) })
	if !ok {
		return fmt.Errorf("this engine has no frequency response")
	}
	st := c.eng.Snapshot()
	ref, d := st.BodeRef&1, st.BodeDut&1
	if dut {
		if ch == ref {
			ref = d
		}
		d = ch
	} else {
		if ch == d {
			d = ref
		}
		ref = ch
	}
	b.SetBodeMode(st.BodeMode != 0, ref, d)
	return nil
}

// bodeClear forgets the frequency-response points (the VIEW tab's action).
// TRLC-LINKS: REQ-SDS-066
func (c *Controller) bodeClear() error {
	b, ok := c.eng.(interface{ ClearBode() })
	if !ok {
		return fmt.Errorf("this engine has no frequency response")
	}
	b.ClearBode()
	c.toast("Bode points cleared", 1200*time.Millisecond)
	return nil
}

// topPeak is the index of the largest peak (-1 none).
// TRLC-LINKS: REQ-SDS-216
func topPeak(ps []Peak) int {
	for i, p := range ps {
		if p.RelDB == 0 {
			return i
		}
	}
	return -1
}

// specMarker is where the marker sits: its frequency, level (dBV) and the
// peak-table row it is on (-1 between peaks).
// TRLC-LINKS: REQ-SDS-216
func specMarker(u *uiState, a *anaState) (f, db float64, row int, ok bool) {
	if !a.specOK {
		return 0, 0, -1, false
	}
	f = u.specF
	if !u.specFSet {
		top := topPeak(a.peaks)
		if top < 0 {
			return 0, 0, -1, false
		}
		f = a.peaks[top].Freq
	}
	row, best := -1, 2.5*a.spec.BinHz
	for i, p := range a.peaks {
		if d := math.Abs(p.Freq - f); d <= best {
			best, row = d, i
		}
	}
	if row >= 0 {
		return f, a.peaks[row].DB, row, true
	}
	k := clampInt(int(math.Round(f/a.spec.BinHz)), 0, len(a.specDBV)-1)
	return f, a.specDBV[k], -1, true
}

// specAdjust is Adjust in SPECTRUM: the marker to the next or previous peak
// (Bode: point).
// TRLC-LINKS: REQ-SDS-216, REQ-SDS-213
func (c *Controller) specAdjust(dir int) {
	s := c.snap()
	if s.viewMode == viewBode {
		c.bodeMove(s, dir)
		return
	}
	c.amu.Lock()
	_, _, row, ok := specMarker(&s.ui, &c.ana)
	peaks := append([]Peak(nil), c.ana.peaks...)
	c.amu.Unlock()
	if !ok || len(peaks) == 0 {
		c.toast("No peaks above the noise floor", 1200*time.Millisecond)
		return
	}
	if row < 0 {
		row = map[bool]int{true: -1, false: 0}[dir > 0]
	}
	i := clampInt(row+dir, 0, len(peaks)-1)
	c.mu.Lock()
	c.ui.specF, c.ui.specFSet = peaks[i].Freq, true
	c.mu.Unlock()
}

// specFine is Position in SPECTRUM: the marker bin by bin (Bode: point).
// TRLC-LINKS: REQ-SDS-216, REQ-SDS-213
func (c *Controller) specFine(dir, n int) {
	s := c.snap()
	if s.viewMode == viewBode {
		c.bodeMove(s, dir*n)
		return
	}
	c.amu.Lock()
	f, _, _, ok := specMarker(&s.ui, &c.ana)
	bin, nyq := c.ana.spec.BinHz, c.ana.spec.SpanHz
	c.amu.Unlock()
	if !ok {
		return
	}
	c.mu.Lock()
	c.ui.specF, c.ui.specFSet = clampF(f+float64(dir*n)*bin, 0, nyq), true
	c.mu.Unlock()
}

// specDelta is the Adjust push in SPECTRUM: a delta reference at the marker,
// or off.
// TRLC-LINKS: REQ-SDS-216
func (c *Controller) specDelta() {
	s := c.snap()
	if s.viewMode == viewBode {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.ui.bodeRef >= 0 {
			c.ui.bodeRef = -1
			c.toastLocked("Delta marker off", 900*time.Millisecond)
			return
		}
		pts := c.bodePoints()
		if len(pts) == 0 {
			return
		}
		c.ui.bodeRef = bodeIndex(&c.ui, len(pts))
		c.toastLocked("Delta reference at "+si(pts[c.ui.bodeRef].FreqHz, "Hz")+" (push again: off)", 1500*time.Millisecond)
		return
	}
	c.amu.Lock()
	f, db, _, ok := specMarker(&s.ui, &c.ana)
	c.amu.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ui.specRefSet {
		c.ui.specRefSet = false
		c.toastLocked("Delta marker off", 900*time.Millisecond)
		return
	}
	if !ok {
		return
	}
	c.ui.specRef, c.ui.specRefDB, c.ui.specRefSet = f, db, true
	c.toastLocked("Delta reference at "+si(f, "Hz")+" (push again: off)", 1500*time.Millisecond)
}

// specLargest is the Position push in SPECTRUM: the marker back to the
// largest peak (Bode: follow the sweep).
// TRLC-LINKS: REQ-SDS-216
func (c *Controller) specLargest() {
	c.mu.Lock()
	c.ui.specFSet, c.ui.bodeIdx = false, -1
	c.toastLocked("Marker: largest peak", 900*time.Millisecond)
	c.mu.Unlock()
}

// bodePoints are the Bode sweep's points (an engine without FRA has none).
// TRLC-LINKS: REQ-SDS-216
func (c *Controller) bodePoints() []engine.BodePoint {
	if b, ok := c.eng.(interface{ BodePoints() []engine.BodePoint }); ok {
		return b.BodePoints()
	}
	return nil
}

// bodeIndex is the marked Bode point: the chosen one, else the newest.
// TRLC-LINKS: REQ-SDS-216
func bodeIndex(u *uiState, n int) int {
	if u.bodeIdx < 0 || u.bodeIdx >= n {
		return n - 1
	}
	return u.bodeIdx
}

// bodeMove moves the Bode marker n points.
// TRLC-LINKS: REQ-SDS-216
func (c *Controller) bodeMove(s *sv, n int) {
	pts := c.bodePoints()
	if len(pts) == 0 {
		return
	}
	c.mu.Lock()
	c.ui.bodeIdx = clampInt(bodeIndex(&c.ui, len(pts))+n, 0, len(pts)-1)
	c.mu.Unlock()
}

// harmName is a peak's harmonic tag: "H3".
// TRLC-LINKS: REQ-SDS-216
func harmName(p Peak) string {
	if p.Harm < 1 {
		return ""
	}
	return fmt.Sprintf("H%d", p.Harm)
}

// specPane builds the SPECTRUM pane.
// TRLC-LINKS: REQ-SDS-216
func (c *Controller) specPane(s *sv) *wsview.SpecPane {
	u := &s.ui
	p := &wsview.SpecPane{BodeIdx: -1, BodeRef: -1, Ch: s.fftSrc}
	p.Title = "SPECTRUM"
	p.Again = "F3 again: " + viewName(nextSpecView(s.viewMode))
	if s.viewMode == viewBode {
		c.bodePane(s, p)
		return p
	}
	c.amu.Lock()
	a := &c.ana
	ok := a.specOK
	f, db, row, mk := specMarker(u, a)
	peaks := append([]Peak(nil), a.peaks...)
	sp, thd, floor := a.spec, a.thd, a.floorDBV
	c.amu.Unlock()
	kind := "FFT"
	switch s.viewMode {
	case viewSpectrogram:
		kind = "SPGM"
	case viewFFT:
	default:
		kind = "FFT (view " + viewName(s.viewMode) + ")"
	}
	p.Right = "S/DIV: span"
	if !ok {
		p.Caption = fmt.Sprintf("%s C%d  waiting for a sampled frame", kind, s.fftSrc+1)
		p.Empty = "No spectrum: the frame on screen has no samples (roll or envelope)"
	} else {
		p.Caption = fmt.Sprintf("%s C%d  span 0-%s  RBW %s  floor %.0fdBV", kind, s.fftSrc+1, si(sp.SpanHz, "Hz"), si(sp.BinHz, "Hz"), floor)
	}
	p.TimeLabel = "TIME"
	if s.zoom > 1 {
		p.TimeLabel = fmt.Sprintf("TIME (ZOOM %dx; FFT: all)", s.zoom)
	}
	p.Head = [4]string{"FREQ", "LEVEL", "VS TOP", "HARM"}
	for i, q := range peaks {
		rel := "0dB"
		if q.RelDB != 0 {
			rel = fmt.Sprintf("%.1fdB", q.RelDB)
		}
		p.Rows = append(p.Rows, wsview.SpecRow{Num: i + 1, Freq: si(q.Freq, "Hz"), Level: fmt.Sprintf("%.1fdBV", q.DB),
			Rel: rel, Harm: harmName(q), Sel: i == row})
	}
	if ok && len(peaks) == 0 {
		p.Empty = "No peaks above the noise floor"
	}
	top := topPeak(peaks)
	if top >= 0 {
		p.Readout = append(p.Readout, wsview.Span{Text: "top " + si(peaks[top].Freq, "Hz"), Col: wsview.Text},
			wsview.Span{Text: fmt.Sprintf("    %.1fdBV", peaks[top].DB), Col: wsview.Text})
		t := "THD ---"
		if !math.IsNaN(thd.Pct) {
			t = fmt.Sprintf("THD %.1f%% (%dh)", thd.Pct, thd.N)
		}
		p.Readout = append(p.Readout, wsview.Span{Text: t, Col: wsview.Text})
	}
	if ok && sp.EffNyq > 0 {
		p.Readout = append(p.Readout, wsview.Span{Text: "fs " + si(2*sp.EffNyq, "Sa/s"), Col: wsview.Dim})
	}
	if mk && top >= 0 {
		p.HasMark, p.MarkHz, p.MarkRel = true, f, db-peaks[top].DB
		p.MarkText = fmt.Sprintf("M %s %.1fdBV", si(f, "Hz"), db)
		t := fmt.Sprintf("MKR %s  %.1fdBV", si(f, "Hz"), db)
		if row >= 0 && peaks[row].Harm > 0 {
			t += "  " + harmName(peaks[row])
		}
		if !u.specFSet {
			t += "  (largest peak)"
		}
		if u.specRefSet {
			p.HasRef, p.RefHz = true, u.specRef
			t = fmt.Sprintf("MKR %s  %.1fdBV   D %s  %.1fdB", si(f, "Hz"), db, sgnSI(f-u.specRef, "Hz"), db-u.specRefDB)
		}
		p.Marker = t
	}
	delta := "delta"
	if u.specRefSet {
		delta = "delta off"
	}
	p.Tool = []wsview.Seg{{Key: "Adjust", Job: "peak"}, {Key: "push", Job: delta}, {Key: "Position", Job: "fine"}, {Key: "push", Job: "largest peak"}}
	return p
}

// bodePane fills the pane with the Bode sweep around the marker.
// TRLC-LINKS: REQ-SDS-216
func (c *Controller) bodePane(s *sv, p *wsview.SpecPane) {
	pts := c.bodePoints()
	u := &s.ui
	p.Bode = true
	p.TimeLabel = "INPUTS"
	p.Head = [4]string{"FREQ", "GAIN", "PHASE", ""}
	if len(pts) == 0 {
		p.Caption = "BODE  sweeping the source (ref C1, DUT C2)..."
		p.Empty = "Sweeping..."
		if why := s.st.BodeWhy; why != "" { // a capture gave no point: say why
			p.Empty = "No point: " + why
		}
	} else {
		p.Caption = fmt.Sprintf("BODE  ref C1, DUT C2  %d points", len(pts))
		if why := s.st.BodeWhy; why != "" {
			p.Caption += "  - " + why
		}
		p.Right = si(pts[0].FreqHz, "Hz") + ".." + si(pts[len(pts)-1].FreqHz, "Hz")
		mi := bodeIndex(u, len(pts))
		p.BodeIdx = mi
		if u.bodeRef >= 0 && u.bodeRef < len(pts) {
			p.BodeRef = u.bodeRef
		}
		i0 := clampInt(mi-3, 0, max(0, len(pts)-7))
		for r := 0; r < 7 && i0+r < len(pts); r++ {
			q := pts[i0+r]
			p.Rows = append(p.Rows, wsview.SpecRow{Num: i0 + r + 1, Freq: si(q.FreqHz, "Hz"),
				Level: fmt.Sprintf("%.1fdB", q.GainDB), Rel: fmt.Sprintf("%.0fdeg", q.PhaseDeg), Sel: i0+r == mi})
		}
		pk, f3 := 0, -1
		for i := range pts {
			if pts[i].GainDB > pts[pk].GainDB {
				pk = i
			}
		}
		for i := 1; i < len(pts); i++ {
			if pts[i].GainDB < pts[0].GainDB-3 {
				f3 = i
				break
			}
		}
		p.Readout = append(p.Readout,
			wsview.Span{Text: fmt.Sprintf("start %.1fdB", pts[0].GainDB), Col: wsview.Text},
			wsview.Span{Text: fmt.Sprintf("peak %.1fdB %s", pts[pk].GainDB, si(pts[pk].FreqHz, "Hz")), Col: wsview.Text})
		if f3 >= 0 {
			p.Readout = append(p.Readout, wsview.Span{Text: "-3dB near " + si(pts[f3].FreqHz, "Hz"), Col: wsview.Text})
		} else {
			p.Readout = append(p.Readout, wsview.Span{Text: "-3dB ---", Col: wsview.Text})
		}
		m := pts[mi]
		t := fmt.Sprintf("MKR %s  %.1fdB  %.0fdeg", si(m.FreqHz, "Hz"), m.GainDB, m.PhaseDeg)
		if p.BodeRef >= 0 {
			r := pts[p.BodeRef]
			t += fmt.Sprintf("   D %s  %.1fdB  %.0fdeg", sgnSI(m.FreqHz-r.FreqHz, "Hz"), m.GainDB-r.GainDB, m.PhaseDeg-r.PhaseDeg)
		}
		p.Marker = t
		p.HasMark, p.MarkHz = true, m.FreqHz
		p.MarkText = fmt.Sprintf("M %s %.1fdB", si(m.FreqHz, "Hz"), m.GainDB)
	}
	delta := "delta"
	if u.bodeRef >= 0 {
		delta = "delta off"
	}
	p.Tool = []wsview.Seg{{Key: "Adjust", Job: "point"}, {Key: "push", Job: delta}, {Key: "Position", Job: "point"}, {Key: "push", Job: "follow the sweep"}}
}

// nextSpecView is SPECTRUM again: FFT, spectrogram, Bode, FFT.
// TRLC-LINKS: REQ-SDS-216
func nextSpecView(v int) int {
	switch v {
	case viewFFT:
		return viewSpectrogram
	case viewSpectrogram:
		return viewBode
	}
	return viewFFT
}
