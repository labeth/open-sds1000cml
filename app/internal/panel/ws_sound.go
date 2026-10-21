// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"fmt"
	"math"
	"time"

	"open-sds/app/internal/analog"
	"open-sds/app/internal/engine"
	"open-sds/app/internal/panel/wsview"
	"open-sds/app/internal/sound"
)

// SOUND is SPECTRUM's fourth view (ADR-SOUND): F3 again cycles FFT,
// spectrogram, Bode and SOUND. The pane holds the tuner (note, cents needle,
// strobe band, the pitch strip), the two level meters and a readout column
// whose page Adjust turns through (modulation, envelope, A/B, harmonics,
// V/oct); the plot below draws the pitch-locked cycle from the FPGA's edge
// marks. Adjust's push runs the page's action (arm the envelope, take a
// V/oct point, swap A/B), Position the cycles drawn, its push the tuned
// channel.

// soundPlotTop is the first plot row under the SOUND pane.
const soundPlotTop = 262

// soundPane builds the SOUND view.
// TRLC-LINKS: REQ-SDS-273
func (c *Controller) soundPane(s *sv) *wsview.SoundPane {
	ss := c.snd
	ss.mu.Lock()
	defer ss.mu.Unlock()
	set := ss.set
	p := &wsview.SoundPane{Ch: set.Ch, Page: soundPageNames[set.Page], PageNum: set.Page, Pages: numSoundPages}
	p.Title = "SOUND"
	p.Again = "F3 again: " + viewName(nextSpecView(viewSound))
	p.Right = fmt.Sprintf("tuner C%d", set.Ch+1)
	in := ss.info
	switch {
	case in.Active:
		p.Caption = fmt.Sprintf("gap-free stream %.1f kSa/s", in.RateHz/1000)
		if in.Losses > 0 {
			losses := "losses"
			if in.Losses == 1 {
				losses = "loss"
			}
			p.Caption += fmt.Sprintf("  %d %s", in.Losses, losses)
		}
	case in.State == "" || in.State == "off":
		p.Caption, p.State = "starting the audio stream", "starting the audio stream (the stream image loads)"
	default:
		p.Caption, p.State = in.State, in.State
		if in.Err != "" {
			p.State += ": " + in.Err
		}
	}
	p.Tool = []wsview.Seg{{Key: "Adjust", Job: "readout"}, {Key: "push", Job: soundPushJob(set.Page)},
		{Key: "Position", Job: "cycles"}, {Key: "push", Job: "C1/C2"}}
	trigFrom := "TRIG"
	if set.TrigGate {
		trigFrom = "the gate"
	}
	switch in.Mode {
	case "stopped":
		p.Caption, p.State = "STOP: the take (RUN: live)", ""
	case "armed", "triggered":
		p.Caption += fmt.Sprintf("  %s on %s, %.2g s / %.2g s", in.Mode, trigFrom, in.PreS, in.PostS)
		p.Tool[2].Job = "trigger in take"
	case "live", "":
	default:
		p.State = in.Mode
	}
	if ss.tv.tk != nil {
		p.Take = ss.takePaneLocked(soundTakeWidth)
		p.Tool[2].Job, p.Tool[3].Job = "scroll", "trigger/all"
		p.Tool = append(p.Tool, wsview.Seg{Key: "S/DIV", Job: "zoom"})
	}
	// The tuner.
	if ss.hz > 0 {
		n := sound.NoteOf(ss.hz, set.A4)
		p.HasPitch, p.Note, p.Cents = true, n.String(), n.Cents
		p.Hz = fmt.Sprintf("%.2f Hz", ss.hz)
		if ss.hz >= 1000 {
			p.Hz = fmt.Sprintf("%.1f Hz", ss.hz)
		}
	} else {
		p.Note, p.Hz = "--", "no pitch"
	}
	p.Sub = fmt.Sprintf("A4 = %g Hz  %s", set.A4, map[string]string{"": "auto", "bass": "bass", "high": "high"}[set.Range])
	p.Strobe = ss.phase
	for _, pt := range ss.strip {
		p.Strip = append(p.Strip, float32(pt.cents))
		p.StripOK = append(p.StripOK, pt.ok)
	}
	// The meters.
	ref := sound.PresetByID(set.Ref)
	p.RefName = "0 dB = " + ref.Name
	for ch := 0; ch < 2; ch++ {
		l := ss.levels[ch]
		m := &p.Meter[ch]
		if !l.OK {
			continue
		}
		m.On, m.DB, m.PeakDB, m.Clip = true, l.DB, l.PeakDB, l.Clip
		m.Hot = l.DB > 0 && set.Ref != "dbu" && set.Ref != "dbv"
		m.Lines = []string{dbText(l.DB, set.Ref), "rms " + si(l.RMS, "V"), "pk " + si(l.Peak, "V"),
			"Vpp " + si(l.Vpp, "V"), "DC " + sgnSI(l.DC, "V"), fmt.Sprintf("crest %.1fdB", l.CrestDB)}
	}
	p.Lines = soundPageLines(ss, set)
	// The cycle trace.
	p.TraceWhy = ss.cycleWhy
	if len(ss.cycles) > 0 && ss.cycleWhy == "" {
		// The pane is built for every LCD tick and every snapshot; its traces
		// only with a new cycle.
		newest := ss.cycles[len(ss.cycles)-1]
		if len(newest) == 0 || ss.pane.n != len(ss.cycles) || ss.pane.newest != &newest[0] {
			ss.pane = soundPaneTraces{n: len(ss.cycles)}
			if len(newest) > 0 {
				ss.pane.newest = &newest[0]
			}
			lo, hi := math.Inf(1), math.Inf(-1)
			for _, tr := range ss.cycles {
				for _, v := range tr {
					lo, hi = math.Min(lo, v), math.Max(hi, v)
				}
			}
			pad := 0.1*(hi-lo) + 1e-6
			ss.pane.lo, ss.pane.hi = lo-pad, hi+pad
			for _, tr := range ss.cycles {
				f := make([]float32, len(tr))
				for i, v := range tr {
					f[i] = float32(v)
				}
				ss.pane.traces = append(ss.pane.traces, f)
			}
		}
		p.TraceLo, p.TraceHi, p.Traces = ss.pane.lo, ss.pane.hi, ss.pane.traces
		cy := ss.cycle
		p.TraceTop = []wsview.Span{{Text: fmt.Sprintf("%d %s, trigger: FPGA edge mark C%d", set.Cycles, plural(set.Cycles, "cycle"), set.Ch+1), Col: wsview.Dim},
			{Text: "period " + si(cy.PeriodS, "s") + "  duty " + fmt.Sprintf("%.1f%%", cy.DutyPct) + "  Vpp " + si(cy.Vpp, "V"), Col: wsview.Text},
			{Text: "rise " + si(cy.RiseS, "s") + "  fall " + si(cy.FallS, "s") + "  slew " + fmt.Sprintf("%.3g V/us", cy.SlewVus), Col: wsview.Text}}
	}
	return p
}

// soundPushJob is the Adjust push's job on a page.
// TRLC-LINKS: REQ-SDS-273
func soundPushJob(page int) string {
	switch page {
	case soundPageADSR:
		return "arm"
	case soundPageAB:
		return "swap in/out"
	case soundPageVOct:
		return "take point"
	}
	return "clear strip"
}

// dbText writes a level against a reference preset: "-6.2 dBu".
// TRLC-LINKS: REQ-SDS-266
func dbText(db float64, ref string) string {
	unit := map[string]string{"dbu": "dBu", "dbv": "dBV"}[ref]
	if unit == "" {
		unit = "dB"
	}
	if math.IsInf(db, -1) || math.IsNaN(db) {
		return "--- " + unit
	}
	return fmt.Sprintf("%.1f %s", db, unit)
}

// pctText writes a percentage, "---" for none.
// TRLC-LINKS: REQ-SDS-273
func pctText(v float64) string {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return "---"
	}
	return fmt.Sprintf("%.2f%%", v)
}

// secText writes seconds with an SI prefix, "---" for none.
// TRLC-LINKS: REQ-SDS-273
func secText(v float64) string {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return "---"
	}
	return si(v, "s")
}

// soundPageLines are the readout column's lines (ss.mu held).
// TRLC-LINKS: REQ-SDS-269, REQ-SDS-268, REQ-SDS-271, REQ-SDS-270, REQ-SDS-267
func soundPageLines(ss *soundSvc, set SoundSettings) []wsview.Span {
	t := func(s string) wsview.Span { return wsview.Span{Text: s, Col: wsview.Text} }
	d := func(s string) wsview.Span { return wsview.Span{Text: s, Col: wsview.Dim} }
	a := func(s string) wsview.Span { return wsview.Span{Text: s, Col: wsview.Amber} }
	var out []wsview.Span
	switch set.Page {
	case soundPageMod:
		m := ss.mod
		switch {
		case ss.hz <= 0 && ss.lfo.OK:
			l := ss.lfo
			out = append(out, a("LFO (no pitch: read directly)"), t("rate "+fmt.Sprintf("%.3g Hz", l.RateHz)+"  "+l.Shape),
				t("Vpp "+si(l.Vpp, "V")+"  mean "+sgnSI(l.Mean, "V")), t(fmt.Sprintf("rise %.0f%% of a cycle", l.RisePct)))
		case !m.OK:
			out = append(out, d("waiting for a tone"))
			if ss.lfo.Why != "" && ss.hz <= 0 {
				out = append(out, d("LFO: "+ss.lfo.Why))
			}
		default:
			out = append(out, a("TREMOLO (AM)"))
			if m.HasAM {
				out = append(out, t(fmt.Sprintf("depth %.1f%%  %.1f dB", m.AMPct, m.AMDB)), t(fmt.Sprintf("rate %.3g Hz  %s", m.AMRate, m.AMShape)))
			} else {
				out = append(out, d(fmt.Sprintf("none (%.1f%%)", m.AMPct)))
			}
			out = append(out, a("VIBRATO (FM)"))
			if m.HasFM {
				out = append(out, t(fmt.Sprintf("+-%.1f cents  +-%.3g Hz", m.FMCents, m.FMHz)), t(fmt.Sprintf("rate %.3g Hz  %s", m.FMRate, m.FMShape)))
			} else {
				out = append(out, d(fmt.Sprintf("none (+-%.1f cents)", m.FMCents)))
			}
			out = append(out, t(fmt.Sprintf("W&F %.3f%% wtd  wow %.3f%%", m.WFPct, m.WowPct)), t(fmt.Sprintf("flutter %.3f%%", m.FlutterPct)))
			if sb := ss.sb; sb.OK && sb.Lines >= 3 && sb.Fit > 0.8 && sb.Beta > 0.2 {
				out = append(out, a("AUDIO-RATE FM"), t(fmt.Sprintf("C:M %s  index %.2f", sb.Ratio, sb.Beta)), d(fmt.Sprintf("fm %s  fit %.2f", si(sb.Fm, "Hz"), sb.Fit)))
			}
		}
	case soundPageADSR:
		gate := "the signal's level"
		if set.Gate >= 0 {
			gate = fmt.Sprintf("gate C%d", set.Gate+1)
		}
		out = append(out, d("trigger: FPGA edge, "+gate+fmt.Sprintf(" %s", si(set.GateV, "V"))))
		switch ss.adsr {
		case adsrIdle:
			out = append(out, d("push Adjust to arm"))
			if ss.adsrWhy != "" {
				out = append(out, d(ss.adsrWhy))
			}
		case adsrArmed:
			out = append(out, a("ARMED: play a note"))
		case adsrCapturing:
			out = append(out, a("capturing..."))
		case adsrDone:
			r := ss.adsrRes
			if !r.OK {
				out = append(out, d("no reading: "+r.Why))
				break
			}
			out = append(out, t("attack "+secText(r.Attack1090)+" (10-90)"), t("  0-100 "+secText(r.Attack0100)))
			if r.HasSustain {
				out = append(out, t("decay "+secText(r.Decay)+"  tau "+secText(r.DecayTau)),
					t(fmt.Sprintf("sustain %.0f%%  %.1f dB", r.SustainPct, r.SustainDB)),
					t("release "+secText(r.Release)+"  tau "+secText(r.ReleaseTau)))
			} else {
				out = append(out, t("decay "+secText(r.Decay)+"  tau "+secText(r.DecayTau)), d("no sustain (percussive)"))
			}
			out = append(out, d("peak "+si(r.Peak, "V")+"  push: arm again"))
		}
	case soundPageAB:
		r := ss.ab
		out = append(out, d(fmt.Sprintf("in C%d  out C%d", set.ABIn+1, 2-set.ABIn)))
		if !r.OK {
			out = append(out, d("waiting: "+r.Why))
			break
		}
		out = append(out, t(fmt.Sprintf("gain %.2f dB at %s", r.GainDB, si(r.F0, "Hz"))), t(fmt.Sprintf("phase %.1f deg", r.PhaseDeg)),
			t(fmt.Sprintf("RMS gain %.2f dB", r.RMSGainDB)))
		if r.DelayOK {
			out = append(out, t("delay "+si(r.DelayS, "s")))
		}
		out = append(out, t("THD in "+pctText(r.THDA)+" out "+pctText(r.THDB)), t("added "+pctText(r.AddedPct)))
	case soundPageHarm:
		h := ss.harm
		if !h.OK {
			out = append(out, d("waiting for a tone"))
			break
		}
		out = append(out, t("H1 "+si(h.F0, "Hz")+fmt.Sprintf("  %.1f dBV", h.H[0].DBV)))
		for i := 1; i < 10; i += 2 {
			l := ""
			for k := i; k < i+2 && k < 10; k++ {
				x := h.H[k]
				v := "  -- "
				if x.Found {
					v = fmt.Sprintf("%6.1f", x.RelDB)
				} else if x.DBV > math.Inf(-1) {
					v = "   ---"
				}
				l += fmt.Sprintf("H%-2d%s  ", k+1, v)
			}
			out = append(out, t(l))
		}
		out = append(out, t("THD "+pctText(h.THDPct)))
		if !math.IsNaN(h.EvenOdd) {
			out = append(out, t(fmt.Sprintf("even/odd %.1f dB", h.EvenOdd)), d(h.Character))
		}
		if h.Hum.Found {
			out = append(out, t(fmt.Sprintf("hum %g Hz %.1f dBV", h.Hum.MainHz, h.Hum.DBV)))
		}
	case soundPageVOct:
		f := sound.FitVOct(ss.voct, set.A4)
		out = append(out, d(fmt.Sprintf("CV C%d %s   VCO C%d", 2-set.Ch, sgnSI(ss.cv, "V"), set.Ch+1)))
		if set.VOctAuto {
			out = append(out, d("auto: points as the CV holds"))
		}
		rows := f.Rows
		if len(rows) > 7 {
			rows = rows[len(rows)-7:]
		}
		for _, r := range rows {
			out = append(out, t(fmt.Sprintf("%6.3fV %-4s %+6.1fc", r.V, r.Note, r.ErrCents)))
		}
		if f.OK {
			out = append(out, a(fmt.Sprintf("scale %+.1fc/V trim %+.2f%%", f.ScaleCents, f.TrimPct)))
			if len(f.Rows) >= 4 {
				out = append(out, t(fmt.Sprintf("top vs low fit %+.1fc", f.HFDevCents)))
			}
		} else if len(f.Rows) < 2 {
			out = append(out, d("push Adjust: take a point"))
		}
	}
	return out
}

// soundAdjust turns the readout page.
// TRLC-LINKS: REQ-SDS-273
func (c *Controller) soundAdjust(dir int) {
	set := c.snd.update(func(s *SoundSettings) { s.Page += dir })
	c.toast("SOUND readout: "+soundPageNames[set.Page], 900*time.Millisecond)
}

// soundPush runs the page's action.
// TRLC-LINKS: REQ-SDS-273, REQ-SDS-268, REQ-SDS-267
func (c *Controller) soundPush() {
	set := c.snd.Settings()
	var err error
	msg := ""
	switch set.Page {
	case soundPageADSR:
		c.snd.mu.Lock()
		busy := c.snd.adsr == adsrArmed || c.snd.adsr == adsrCapturing
		c.snd.mu.Unlock()
		c.snd.arm(!busy)
		msg = map[bool]string{true: "Envelope: cancelled", false: "Envelope: armed (the FPGA's edge starts it)"}[busy]
	case soundPageAB:
		set = c.snd.update(func(s *SoundSettings) { s.ABIn = 1 - s.ABIn })
		msg = fmt.Sprintf("A/B: in C%d, out C%d", set.ABIn+1, 2-set.ABIn)
	case soundPageVOct:
		err = c.snd.voctCapture()
		msg = "V/oct: point taken"
	default:
		err = c.snd.action("strip_clear")
		msg = "Pitch strip cleared"
	}
	if err != nil {
		c.toast(err.Error(), 1500*time.Millisecond)
		return
	}
	c.toast(msg, 1200*time.Millisecond)
}

// soundPosition scrolls a take shown, moves the trigger within the take
// while a triggered mode is armed, else steps the cycles drawn: 1, 2, 4.
// TRLC-LINKS: REQ-SDS-272, REQ-SDS-273, REQ-SDS-275
func (c *Controller) soundPosition(dir int) {
	if c.snd.takeScroll(dir) {
		return
	}
	c.snd.mu.Lock()
	mode := c.snd.info.Mode
	c.snd.mu.Unlock()
	if mode == "armed" || mode == "triggered" {
		set := c.snd.moveTrigger(dir)
		c.toast(fmt.Sprintf("Take: %.2g s before the trigger, %.2g s after", set.PreS, set.PostS), 900*time.Millisecond)
		return
	}
	set := c.snd.update(func(s *SoundSettings) {
		order := []int{1, 2, 4}
		i := 1
		for k, v := range order {
			if v == s.Cycles {
				i = k
			}
		}
		s.Cycles = order[clampInt(i+dir, 0, 2)]
	})
	c.snd.mu.Lock()
	c.snd.cycles = nil
	c.snd.mu.Unlock()
	c.toast(fmt.Sprintf("Cycle trace: %d %s", set.Cycles, plural(set.Cycles, "cycle")), 900*time.Millisecond)
}

// soundChannel puts a take's trigger at the centre (or shows all of the
// take); live, it swaps the tuned channel.
// TRLC-LINKS: REQ-SDS-267, REQ-SDS-273, REQ-SDS-275
func (c *Controller) soundChannel() {
	if msg := c.snd.takeToTrigger(); msg != "" {
		c.toast(msg, 900*time.Millisecond)
		return
	}
	set := c.snd.update(func(s *SoundSettings) { s.Ch = 1 - s.Ch })
	c.toast(fmt.Sprintf("Tuner: C%d", set.Ch+1), 900*time.Millisecond)
}

// SoundSettings returns SOUND's settings (the web page and the phone).
// TRLC-LINKS: REQ-SDS-274
func (c *Controller) SoundSettings() SoundSettings { return c.snd.Settings() }

// SetSoundSettings replaces SOUND's settings (bounded) and returns them.
// TRLC-LINKS: REQ-SDS-274
func (c *Controller) SetSoundSettings(s SoundSettings) SoundSettings {
	return c.snd.update(func(cur *SoundSettings) { *cur = s })
}

// SoundAction runs a SOUND action by name ("adsr_arm", "voct_capture"...).
// TRLC-LINKS: REQ-SDS-274
func (c *Controller) SoundAction(name string) error { return c.snd.action(name) }

// SoundWant asks the service to keep the shared readouts current (api) or
// the FPGA marking the tuned channel's edges (marks) for a few seconds.
// TRLC-LINKS: REQ-SDS-274
func (c *Controller) SoundWant(api, marks bool) { c.snd.want(api, marks, 3*time.Second) }

// SoundReport snapshots the shared SOUND readouts; api asks the service to
// keep them current (the tuner and meters) for a few seconds, marks to keep
// the FPGA marking the tuned channel's edges (a page's cycle view).
// TRLC-LINKS: REQ-SDS-274
func (c *Controller) SoundReport(api, marks bool) SoundReport {
	c.snd.want(api, marks, 3*time.Second)
	return c.snd.Report()
}

// soundAutoFit is AUTO in SOUND: each channel's V/div and offset fitted to
// the stream, the signal about six divisions tall and centred (a clipped
// channel steps two detents up first), settling between steps. Triggered
// captures stay paused: the stream keeps running.
// TRLC-LINKS: REQ-SDS-273, REQ-SDS-266
func (c *Controller) soundAutoFit(stop chan struct{}) {
	e, ok := c.eng.(soundEngine)
	if !ok || c.fe == nil {
		c.toast("AUTO: no audio stream here", autoNoticeFor)
		return
	}
	e.LeaseSound(3 * time.Second)
	var done [2]bool
	active, state := false, ""
	for try := 0; try < 8 && !(done[0] && done[1]); try++ {
		select {
		case <-stop:
			return
		case <-time.After(400 * time.Millisecond): // the front end settles, new samples come
		}
		e.LeaseSound(3 * time.Second)
		info := e.SoundInfo()
		if state = info.State; !info.Active || info.RateHz <= 0 {
			continue
		}
		active = true
		b := e.SoundLatest(int(0.3*info.RateHz), nil, nil)
		idx, _ := c.fe.Snapshot()
		for ch := 0; ch < 2; ch++ {
			if done[ch] {
				continue
			}
			q := b.Q1
			if ch == 1 {
				q = b.Q2
			}
			if sound.ClipCodes(q) {
				_ = c.setVdivIdx(ch, idx[ch]+2)
				continue
			}
			l := sound.Measure(c.snd.volts(b, ch, nil))
			p := c.fe.ProbeFactor(ch)
			want := len(analog.Detents) - 1
			for i, d := range analog.Detents {
				if d.VdivV*p*6 >= l.Vpp {
					want = i
					break
				}
			}
			c.fe.SetOffset(ch, -(l.Max+l.Min)/2/p)
			done[ch] = want == idx[ch]
			_ = c.setVdivIdx(ch, want)
		}
	}
	switch {
	case !active:
		c.toast("AUTO: no audio stream: "+state, autoNoticeFor)
	case !(done[0] && done[1]):
		c.toast("AUTO: the level kept moving; fitted to the last reading", autoNoticeFor)
	default:
		c.toast("AUTO: both channels fitted to the stream", autoNoticeFor)
	}
}

// SoundRemoteLease is a page's SOUND request: it records the page showing
// SOUND and, unless the LCD took the acquisition back, runs lease (the
// page's hold on the stream) and reports true; false is the page paused.
// who is "phone" for the phone remote (the LCD's notice names it).
// TRLC-LINKS: REQ-SDS-277
func (c *Controller) SoundRemoteLease(who string, lease func()) bool {
	return c.snd.remoteLease(who, lease)
}

// SoundPaused reports the pages' SOUND leases refused (the LCD holds the
// acquisition until a page takes the scope).
// TRLC-LINKS: REQ-SDS-277
func (c *Controller) SoundPaused() bool { return c.snd.remotePaused() }

// lcdInSoundLocked reports SOUND as the LCD's workspace (c.mu held).
// TRLC-LINKS: REQ-SDS-277
func (c *Controller) lcdInSoundLocked() bool {
	return c.ui.ws == wsview.Spectrum && c.viewMode == viewSound
}

// lcdActs is the LCD's key or knob acting on the scope: the acquisition is
// the LCD's from now (the last one to act wins), so a page's audio stream
// ends and its leases are refused until it takes the scope again; on the
// SOUND view the stream is the LCD's own and runs on.
// TRLC-LINKS: REQ-SDS-277
func (c *Controller) lcdActs() {
	c.mu.Lock()
	in := c.lcdInSoundLocked()
	c.mu.Unlock()
	c.snd.lcdTake(in)
}

// lcdButton runs a key pressed on the LCD (or injected as one): the
// acquisition taken before it (an AUTO's sweep then reads captures) and
// again after it (a key leaving SOUND).
// TRLC-LINKS: REQ-SDS-277, REQ-SDS-135
func (c *Controller) lcdButton(code int) {
	c.lcdActs()
	c.button(code)
	c.lcdActs()
}

// lcdKnob runs a knob turned on the LCD (or injected as one), the
// acquisition taken as for a key.
// TRLC-LINKS: REQ-SDS-277, REQ-SDS-135
func (c *Controller) lcdKnob(name string, dir, steps int) {
	c.lcdActs()
	c.dispatch(name, dir, steps)
	c.lcdActs()
}

// soundOffWait bounds waitSoundOff.
const soundOffWait = 3 * time.Second

// waitSoundOff waits (bounded, or until stop) for an audio stream that
// gave the fabric back to stop, so a sweep's first captures are captures.
// TRLC-LINKS: REQ-SDS-277
func (c *Controller) waitSoundOff(stop chan struct{}) {
	e, ok := c.eng.(interface{ SoundInfo() engine.SoundInfo })
	if !ok {
		return
	}
	end := time.Now().Add(soundOffWait)
	for e.SoundInfo().Active && time.Now().Before(end) {
		select {
		case <-stop:
			return
		case <-time.After(50 * time.Millisecond):
		}
	}
}
