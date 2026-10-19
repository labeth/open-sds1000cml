// ENGMODEL-OWNER-UNIT: FU-APP-LCD
package lcd

import (
	"math"
	"testing"

	"open-sds/app/internal/panel/wsview"
)

// goldenSoundPane is a SOUND view as the panel builds it: A4 3 cents sharp
// on C1, both meters, a cycle trace with its persistence.
// TRLC-LINKS: REQ-SDS-273
func goldenSoundPane(page string, lines []wsview.Span) *wsview.SoundPane {
	s := &wsview.SoundPane{Pane: wsview.Pane{Title: "SOUND", Caption: "gap-free stream 61.0 kSa/s", Right: "tuner C1",
		Tool:  []wsview.Seg{{Key: "Adjust", Job: "readout"}, {Key: "push", Job: "clear strip"}, {Key: "Position", Job: "cycles"}, {Key: "push", Job: "C1/C2"}},
		Again: "F3 again: FFT"},
		HasPitch: true, Note: "A4", Cents: 3.1, Hz: "440.79 Hz", Sub: "A4 = 440 Hz  auto", Strobe: 0.3,
		RefName: "0 dB = dBu", Page: page, PageNum: 0, Pages: 5, Lines: lines}
	for i := 0; i < 80; i++ {
		s.Strip = append(s.Strip, float32(3+2*math.Sin(float64(i)/9)))
		s.StripOK = append(s.StripOK, i > 4)
	}
	s.Meter[0] = wsview.SoundMeter{On: true, DB: -6.2, PeakDB: -3.1, Lines: []string{"-6.2 dBu", "rms 380mV", "pk 537mV", "Vpp 1.07V", "DC +2mV", "crest 3.0dB"}}
	s.Meter[1] = wsview.SoundMeter{On: true, DB: 2.4, PeakDB: 5.5, Clip: true, Lines: []string{"2.4 dBu", "rms 1.02V", "pk 1.6V", "Vpp 3.2V", "DC -10mV", "crest 3.9dB"}}
	for k := 0; k < 4; k++ {
		tr := make([]float32, 600)
		for i := range tr {
			ph := 2 * math.Pi * 2 * float64(i) / 599
			v := 0.0
			for h := 1; h < 12; h += 2 {
				v += math.Sin(float64(h)*ph) / float64(h)
			}
			tr[i] = float32(0.5*v + 0.02*float64(k))
		}
		s.Traces = append(s.Traces, tr)
	}
	s.TraceLo, s.TraceHi = -0.55, 0.6
	s.TraceTop = []wsview.Span{{Text: "2 cycles, trigger: FPGA edge mark C1", Col: wsview.Dim},
		{Text: "period 2.27ms  duty 50.0%  Vpp 1.07V", Col: wsview.Text}, {Text: "rise 98us  fall 98us  slew 0.0315 V/us", Col: wsview.Text}}
	return s
}

// goldenSoundRender renders a SOUND pane in SPECTRUM.
// TRLC-LINKS: REQ-SDS-273
func goldenSoundRender(t *testing.T, name string, s *wsview.SoundPane) {
	t.Helper()
	v := wsview.View{WS: wsview.Spectrum, Layout: wsview.Layout{MenuW: 30, PlotTop: 262, HideViewCaptions: true, HideBottom: true, HideMeas: true},
		Rail: true, Sound: s}
	h := goldenUIHUD(v)
	h.ViewMode = 5
	goldenRender(t, name, goldenYTFrame(), h)
}

// TestGoldenSoundModulation pins the SOUND view on its modulation page:
// tuner, meters (C2 clipping), the readout and the pitch-locked trace.
// TRLC-LINKS: REQ-SDS-273, REQ-SDS-269, REQ-SDS-272
func TestGoldenSoundModulation(t *testing.T) {
	goldenSoundRender(t, "ws_sound_mod", goldenSoundPane("MODULATION", []wsview.Span{
		{Text: "TREMOLO (AM)", Col: wsview.Amber}, {Text: "depth 30.0%  5.4 dB", Col: wsview.Text}, {Text: "rate 5 Hz  sine", Col: wsview.Text},
		{Text: "VIBRATO (FM)", Col: wsview.Amber}, {Text: "+-20.0 cents  +-5.09 Hz", Col: wsview.Text}, {Text: "rate 6 Hz  sine", Col: wsview.Text},
		{Text: "W&F 0.312% wtd  wow 0.204%", Col: wsview.Text}, {Text: "flutter 0.811%", Col: wsview.Text}}))
}

// TestGoldenSoundEnvelope pins the envelope page after a capture, with no
// pitch (the tuner idle) and no cycle trace.
// TRLC-LINKS: REQ-SDS-273, REQ-SDS-268
func TestGoldenSoundEnvelope(t *testing.T) {
	s := goldenSoundPane("ENVELOPE", []wsview.Span{{Text: "trigger: FPGA edge, gate C2 1.5V", Col: wsview.Dim},
		{Text: "attack 40ms (10-90)", Col: wsview.Text}, {Text: "  0-100 50ms", Col: wsview.Text}, {Text: "decay 92.1ms  tau 40ms", Col: wsview.Text},
		{Text: "sustain 50%  -6.0 dB", Col: wsview.Text}, {Text: "release 184ms  tau 80ms", Col: wsview.Text}, {Text: "peak 4.98V  push: arm again", Col: wsview.Dim}})
	s.PageNum, s.HasPitch, s.Note, s.Hz, s.Strip, s.StripOK = 1, false, "--", "no pitch", nil, nil
	s.Traces, s.TraceWhy = nil, "no pitch to lock to"
	goldenSoundRender(t, "ws_sound_env", s)
}

// TestGoldenSoundHarmonics pins the harmonics page with the stream not yet
// running.
// TRLC-LINKS: REQ-SDS-273, REQ-SDS-270
func TestGoldenSoundHarmonics(t *testing.T) {
	s := goldenSoundPane("HARMONICS", []wsview.Span{{Text: "H1 1kHz  -0.0 dBV", Col: wsview.Text},
		{Text: "H2  -40.0  H3  -30.0  ", Col: wsview.Text}, {Text: "H4    --   H5  -52.1  ", Col: wsview.Text},
		{Text: "THD 3.32%", Col: wsview.Text}, {Text: "even/odd -10.0 dB", Col: wsview.Text}, {Text: "odd (symmetric)", Col: wsview.Dim},
		{Text: "hum 50 Hz -60.0 dBV", Col: wsview.Text}})
	s.PageNum, s.Cents, s.Note = 3, -27, "C#3"
	s.State = "starting the audio stream (the stream image loads)"
	s.Traces, s.TraceWhy = nil, "waiting for the edge marks"
	goldenSoundRender(t, "ws_sound_harm", s)
}
