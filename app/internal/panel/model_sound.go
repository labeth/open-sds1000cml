// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"fmt"
	"math"

	"open-sds/app/internal/panel/wsview"
	"open-sds/app/internal/sound"
)

// The SOUND tab: SOUND's settings as sheet rows, so the LCD's sheet, the
// phone's menus and typed entry change them through one setter each.

// soundRowInfo is the service state the SOUND rows show.
type soundRowInfo struct {
	adsr   string
	points int
	drift  int
}

// rowInfo snapshots it.
// TRLC-LINKS: REQ-SDS-273
func (s *soundSvc) rowInfo() soundRowInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	return soundRowInfo{adsr: s.adsr, points: len(s.voct), drift: len(s.drift.Points)}
}

// sndEnum is an enum row over one SOUND setting.
// TRLC-LINKS: REQ-SDS-273
func sndEnum(id, label, help string, opts []opt, get func(SoundSettings) int, set func(*SoundSettings, int)) *row {
	return &row{id: id, label: label, kind: wsview.Enum, help: help, opts: staticOpts(opts),
		get: func(s *sv) int { return get(s.snd) },
		set: func(c *Controller, v int) error {
			c.snd.update(func(st *SoundSettings) { set(st, v) })
			return nil
		},
		def: func(c *Controller) error {
			d := defaultSoundSettings()
			c.snd.update(func(st *SoundSettings) { set(st, get(d)) })
			return nil
		},
	}
}

// soundRows are the SOUND tab's rows.
// TRLC-LINKS: REQ-SDS-273
func soundRows() []*row {
	var refs []opt
	for i, p := range sound.Presets {
		refs = append(refs, opt{i, p.Name})
	}
	refIndex := func(id string) int {
		for i, p := range sound.Presets {
			if p.ID == id {
				return i
			}
		}
		return 0
	}
	var pages []opt
	for i, n := range soundPageNames {
		pages = append(pages, opt{i, n})
	}
	ch := sndEnum("snd.ch", "Tuned channel", "The channel the tuner, the cycle trace, modulation and harmonics read (the VCO for V/oct, its CV on the other).",
		chans, func(s SoundSettings) int { return s.Ch }, func(s *SoundSettings, v int) { s.Ch = v & 1 })
	ch.hdr = "TUNER  (SPECTRUM: F3 again to SOUND)"
	ch.knob = "In the SOUND view: push Position."
	a4 := numRow("snd.a4", "A4", "Hz", func(s *sv) float64 { return s.snd.A4 }, constLim(400, 480), constStep(1),
		func(c *Controller, v float64) error {
			c.snd.update(func(s *SoundSettings) { s.A4 = math.Round(v) })
			return nil
		}, siFmt("Hz"))
	a4.help = "The tuner's reference: the pitch named A4."
	a4.presets = func(*sv) []preset {
		return []preset{{t: "440 Hz", v: 440}, {t: "432 Hz", v: 432}, {t: "442 Hz", v: 442}}
	}
	a4.def = func(c *Controller) error { return a4.setN(c, 440) }
	rng := sndEnum("snd.range", "Range", "The tuner's search: auto 25 Hz-5 kHz, bass 20-600 Hz, high 200 Hz-12 kHz.",
		[]opt{{0, "Auto"}, {1, "Bass"}, {2, "High"}},
		func(s SoundSettings) int { return map[string]int{"": 0, "bass": 1, "high": 2}[s.Range] },
		func(s *SoundSettings, v int) { s.Range = []string{"", "bass", "high"}[clampInt(v, 0, 2)] })
	slow := sndEnum("snd.slow", "Smoothing", "Fast follows the pitch (three readings); slow holds it steadier (nine).",
		[]opt{{0, "Fast"}, {1, "Slow"}}, func(s SoundSettings) int { return b2ic(s.Slow) }, func(s *SoundSettings, v int) { s.Slow = v == 1 })
	ref := sndEnum("snd.ref", "Meter 0 dB", "What the meters read as 0 dB: dBu (0.775 V), dBV (1 V), a Eurorack 10 Vpp sine, +4 dBu, -10 dBV, a guitar's 100 mV.",
		refs, func(s SoundSettings) int { return refIndex(s.Ref) }, func(s *SoundSettings, v int) { s.Ref = sound.Presets[clampInt(v, 0, len(sound.Presets)-1)].ID })
	ref.hdr = "METERS AND READOUTS"
	page := sndEnum("snd.page", "Readout", "The SOUND view's readout column: modulation, envelope, A/B, harmonics or V/oct.",
		pages, func(s SoundSettings) int { return s.Page }, func(s *SoundSettings, v int) { s.Page = v })
	page.knob = "In the SOUND view: turn Adjust."
	cyc := sndEnum("snd.cycles", "Cycles drawn", "Periods of the pitch-locked trace; its trigger is the FPGA's edge on the tuned channel.",
		[]opt{{1, "1"}, {2, "2"}, {4, "4"}}, func(s SoundSettings) int { return s.Cycles }, func(s *SoundSettings, v int) { s.Cycles = v })
	cyc.knob = "In the SOUND view: turn Position."
	abIn := sndEnum("snd.ab_in", "A/B input", "The circuit's input channel; its output on the other.",
		chans, func(s SoundSettings) int { return s.ABIn }, func(s *SoundSettings, v int) { s.ABIn = v & 1 })
	env := sndEnum("snd.env", "Envelope of", "A tone's amplitude, or a CV read as the envelope itself (an envelope generator's output).",
		[]opt{{0, "Tone"}, {1, "CV"}}, func(s SoundSettings) int { return b2ic(s.EnvCV) }, func(s *SoundSettings, v int) { s.EnvCV = v == 1 })
	env.hdr = "ENVELOPE (ADSR)"
	gate := sndEnum("snd.gate", "Envelope trigger", "The FPGA's edge that starts a capture: the signal crossing its level, or a gate on C1 or C2 (its fall ends the gate).",
		[]opt{{-1, "Signal"}, {0, "Gate C1"}, {1, "Gate C2"}}, func(s SoundSettings) int { return s.Gate }, func(s *SoundSettings, v int) { s.Gate = clampInt(v, -1, 1) })
	gateV := numRow("snd.gate_v", "Trigger level", "V", func(s *sv) float64 { return s.snd.GateV }, constLim(-20, 20), constStep(0.05),
		func(c *Controller, v float64) error {
			c.snd.update(func(s *SoundSettings) { s.GateV = v })
			return nil
		}, siFmt("V"))
	gateV.help = "A gate's level, or how far above its quiet level the signal must rise."
	gateV.def = func(c *Controller) error { return gateV.setN(c, 0.1) }
	arm := &row{id: "snd.adsr", label: "Capture", kind: wsview.Action,
		help: "Arm: the next edge of the trigger starts an envelope capture; it ends when the sound has died away.",
		knob: "In the SOUND view's envelope page: push Adjust.",
		acts: func(s *sv) []act {
			return []act{{id: "arm", t: "Arm", run: func(c *Controller) error { return c.snd.action("adsr_arm") }},
				{id: "cancel", t: "Cancel", dim: s.sndRow.adsr != adsrArmed && s.sndRow.adsr != adsrCapturing,
					run: func(c *Controller) error { return c.snd.action("adsr_cancel") }}}
		},
		status: func(s *sv) string { return s.sndRow.adsr },
	}
	auto := sndEnum("snd.voct_auto", "V/oct points", "By hand (push Adjust on the V/oct page) or taken by themselves once the CV and the pitch hold still.",
		[]opt{{0, "By hand"}, {1, "Auto"}}, func(s SoundSettings) int { return b2ic(s.VOctAuto) }, func(s *SoundSettings, v int) { s.VOctAuto = v == 1 })
	auto.hdr = "V/OCT AND DRIFT"
	voct := &row{id: "snd.voct", label: "V/oct table", kind: wsview.Action,
		help: "Take a point now (the CV on the other channel, the VCO's pitch), undo the last, or start over.",
		acts: func(*sv) []act {
			return []act{{id: "take", t: "Take", run: func(c *Controller) error { return c.snd.action("voct_capture") }},
				{id: "undo", t: "Undo", run: func(c *Controller) error { return c.snd.action("voct_undo") }},
				{id: "clear", t: "Clear", run: func(c *Controller) error { return c.snd.action("voct_clear") }}}
		},
		status: func(s *sv) string { return fmt.Sprintf("%d %s", s.sndRow.points, plural(s.sndRow.points, "point")) },
	}
	drift := sndEnum("snd.drift", "Drift log", "Log the tuned pitch once a second (a VCO warming up).",
		onOff, func(s SoundSettings) int { return b2ic(s.Drift) }, func(s *SoundSettings, v int) { s.Drift = v == 1 })
	driftClear := &row{id: "snd.drift_clear", label: "Drift points", kind: wsview.Action,
		help: "Start the drift log over.",
		acts: func(*sv) []act {
			return []act{{id: "clear", t: "Clear", run: func(c *Controller) error { return c.snd.action("drift_clear") }}}
		},
		status: func(s *sv) string { return fmt.Sprintf("%d s", s.sndRow.drift) },
	}
	return []*row{ch, a4, rng, slow, ref, page, cyc, abIn, env, gate, gateV, arm, auto, voct, drift, driftClear}
}
