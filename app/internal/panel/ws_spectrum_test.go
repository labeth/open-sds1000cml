// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"math"
	"strings"
	"testing"

	"open-sds/app/internal/engine"
	"open-sds/app/internal/measure"
)

// SPECTRUM: the peak table finds the square wave's odd harmonics with THD,
// the Adjust push sets a delta reference (again: off), the source follows
// the VIEW tab, and in Bode the knobs move the marker point by point.
// TRLC-LINKS: REQ-SDS-216
func TestSpectrumTools(t *testing.T) {
	r := newViewRig(t)
	c := r.c
	press(t, c, "f3")
	r.show(toneFrame(""))
	v := c.View().Spec
	if len(v.Rows) < 4 || v.Rows[0].Freq != "1kHz" || v.Rows[0].Harm != "H1" || v.Rows[1].Harm != "H3" || v.Rows[0].Rel != "0dB" ||
		!strings.HasPrefix(v.Readout[2].Text, "THD ") {
		t.Fatalf("peaks %+v readout %+v", v.Rows, v.Readout)
	}
	turn(c, "adjust", 1)
	press(t, c, "adjustpush")
	if v := c.View().Spec; !v.HasRef || !strings.Contains(v.Marker, " D ") {
		t.Fatalf("delta: %+v", v)
	}
	press(t, c, "adjustpush")
	if v := c.View().Spec; v.HasRef {
		t.Fatal("delta not off")
	}
	c.SheetSet("view.fft_source", nil, "CH2")
	r.show(toneFrame(""))
	if v := c.View().Spec; v.Ch != 1 || v.Rows[0].Freq != "3kHz" {
		t.Fatalf("CH2 source: %+v", v.Rows)
	}
	r.eng.pts = []engine.BodePoint{{FreqHz: 100}, {FreqHz: 1e3, GainDB: -1}, {FreqHz: 1e4, GainDB: -10}}
	press(t, c, "f3", "f3") // spectrogram, Bode
	if v := c.View().Spec; !v.Bode || v.BodeIdx != 2 {
		t.Fatalf("Bode follows the sweep: %+v", v)
	}
	turn(c, "adjust", -1)
	turn(c, "horizpos", -1)
	if v := c.View().Spec; v.BodeIdx != 0 || !strings.HasPrefix(v.Marker, "MKR 100Hz") {
		t.Fatalf("Bode marker %d %q", v.BodeIdx, v.Marker)
	}
	press(t, c, "horizpospush")
	if v := c.View().Spec; v.BodeIdx != 2 {
		t.Fatalf("Position push: %d", v.BodeIdx)
	}
}

// MEASURE: entering with nothing chosen picks Vpp and frequency of the
// shown channels; F4 again shows every value of both channels.
// TRLC-LINKS: REQ-SDS-217
func TestMeasureViews(t *testing.T) {
	r := newViewRig(t)
	c := r.c
	press(t, c, "f4")
	r.show(toneFrame(""))
	v := c.View().Meas
	if len(v.Stats) != 4 || v.Stats[0].Name != "C1 Vpp" || v.Stats[3].Name != "C2 Freq" || v.Stats[1].Now != "1kHz" {
		t.Fatalf("stats %+v", v.Stats)
	}
	press(t, c, "f4")
	v = c.View().Meas
	// the web's names, order and columns, preshoot too
	var names []string
	for _, r := range v.AllRows[:v.AllN] {
		names = append(names, r[0])
	}
	want := "Vpp Vmax Vmin Vmean Vrms Freq Period Duty ACrms Vtop Vbase Vampl Rise Fall +Width -Width Overshoot Preshoot"
	if !v.All || v.AllN != len(measParams) || strings.Join(names, " ") != want || v.AllSplit != 9 ||
		v.AllRows[5][1] != "1kHz" || v.AllRows[5][2] != "3kHz" {
		t.Fatalf("all values %d split %d: %q, Freq %+v", v.AllN, v.AllSplit, names, v.AllRows[5])
	}
}

// SPECTRUM's FFT and spectrogram ask for the frames a spectrum can be taken
// of, as the web's FFT does (the full-rate record on short screens, else the
// filtered decimated one), so both surfaces transform the same samples, not
// the live envelope; Bode keeps its own frames.
// TRLC-LINKS: REQ-SDS-070, REQ-SDS-216
func TestSpectrumLeasesSpectrumFrames(t *testing.T) {
	r := newViewRig(t)
	c := r.c
	press(t, c, "f3") // FFT
	n0 := r.eng.specLeases.Load()
	r.show(toneFrame(""))
	if r.eng.specLeases.Load() == n0 {
		t.Fatal("the FFT did not lease spectrum frames")
	}
	press(t, c, "f3") // spectrogram
	n1 := r.eng.specLeases.Load()
	r.show(toneFrame(""))
	if r.eng.specLeases.Load() == n1 {
		t.Fatal("the spectrogram did not lease spectrum frames")
	}
	press(t, c, "f3") // Bode
	n2 := r.eng.specLeases.Load()
	r.show(toneFrame(""))
	if r.eng.specLeases.Load() != n2 {
		t.Fatal("Bode leased spectrum frames")
	}
	// leaving them gives the lease back at once: the decimated spectrum frames
	// are filtered, and MEASURE's statistics took them for 3 s after
	if r.eng.specReleases.Load() != 1 {
		t.Fatalf("Bode: %d releases, want 1", r.eng.specReleases.Load())
	}
	press(t, c, "f3") // FFT again
	r.show(toneFrame(""))
	press(t, c, "f4") // MEASURE
	r.show(toneFrame(""))
	if r.eng.specReleases.Load() != 2 {
		t.Fatalf("MEASURE: %d releases, want 2", r.eng.specReleases.Load())
	}
}

// Timing taken on a peak-detect envelope holds no edge shape: overshoot is
// unknown ("---"), as rise and fall are and as the web shows it, not "0%";
// a sampled capture's overshoot reads.
// TRLC-LINKS: REQ-SDS-217
func TestOvershootUnknownOnEnvelopeTiming(t *testing.T) {
	i := measIndex("ovsh")
	env := &measure.Result{HasTiming: true, EnvTiming: true}
	if v := measValue(env, i); !math.IsNaN(v) || fmtMeas(i, v) != "---" {
		t.Fatalf("envelope timing: overshoot %g (%q)", v, fmtMeas(i, v))
	}
	sampled := &measure.Result{HasTiming: true, Overshoot: 3.5}
	if v := measValue(sampled, i); v != 3.5 {
		t.Fatalf("sampled: overshoot %g", v)
	}
}

// The scope sets the frequency response's channels and clears its points (the
// VIEW tab's Bode rows, as the web's selectors and Clear; the LCD's Bode view
// arms the chosen channels): picking the other row's channel swaps the two,
// the armed state stays, DEFAULT SETUP restores C1 / C2 disarmed and forgets
// the points.
// TRLC-LINKS: REQ-SDS-066
func TestBodeChannelsAndClear(t *testing.T) {
	r := newViewRig(t)
	c := r.c
	r.eng.SetBodeMode(true, 0, 1)
	r.eng.pts = []engine.BodePoint{{FreqHz: 1e3}, {FreqHz: 1e4}}
	r.eng.set(func(st *engine.Stats) { st.BodePoints = 2 })
	if res := c.SheetSet("view.bode_ref", nil, "C2"); !res.OK {
		t.Fatalf("Bode REF C2: %+v", res)
	}
	if st := r.eng.Snapshot(); st.BodeRef != 1 || st.BodeDut != 0 || st.BodeMode != 1 {
		t.Fatalf("REF C2: ref %d dut %d mode %d, want 1 0 armed", st.BodeRef, st.BodeDut, st.BodeMode)
	}
	if res := c.SheetSet("view.bode_dut", nil, "C2"); !res.OK {
		t.Fatalf("Bode DUT C2: %+v", res)
	}
	if st := r.eng.Snapshot(); st.BodeRef != 0 || st.BodeDut != 1 {
		t.Fatalf("DUT C2: ref %d dut %d, want 0 1", st.BodeRef, st.BodeDut)
	}
	if got := rowText(c, "view.bode_clear"); got != "2 points" {
		t.Fatalf("clear row status %q", got)
	}
	if res := c.SheetAction("view.bode_clear", "clear"); !res.OK || len(r.eng.pts) != 0 {
		t.Fatalf("clear: %+v, %d points left", res, len(r.eng.pts))
	}
	r.eng.SetBodeMode(true, 1, 0)
	c.DefaultSetup()
	if st := r.eng.Snapshot(); st.BodeMode != 0 || st.BodeRef != 0 || st.BodeDut != 1 {
		t.Fatalf("DEFAULT SETUP: mode %d ref %d dut %d", st.BodeMode, st.BodeRef, st.BodeDut)
	}
}

// A marker left above the span a slower timebase gives goes back to the
// largest peak: it named a frequency the spectrum no longer holds, with the
// level of the span's top bin.
// TRLC-LINKS: REQ-SDS-216
func TestSpectrumMarkerOutsideTheSpan(t *testing.T) {
	r := newViewRig(t)
	c := r.c
	press(t, c, "f3")
	r.show(toneFrame(""))
	c.amu.Lock()
	span := c.ana.spec.SpanHz
	c.amu.Unlock()
	c.mu.Lock()
	c.ui.specF, c.ui.specFSet = 0.9*span, true
	c.mu.Unlock()
	slow := toneFrame("")
	slow.SampleS *= 10
	slow.TdivS *= 10
	r.show(slow)
	v := c.View().Spec
	if !v.HasMark || v.MarkHz > span/10 || !strings.Contains(v.Marker, "(largest peak)") {
		t.Fatalf("marker at %s of a %s span: %q", si(v.MarkHz, "Hz"), si(span/10, "Hz"), v.Marker)
	}
}

// SPECTRUM's Bode caption names the reference and DUT channels chosen (the
// VIEW tab swaps them), not C1 / C2 always.
// TRLC-LINKS: REQ-SDS-066
func TestBodeCaptionNamesTheChannels(t *testing.T) {
	r := newViewRig(t)
	c := r.c
	press(t, c, "f3", "f3", "f3") // FFT, spectrogram, Bode
	r.eng.SetBodeMode(true, 1, 0)
	if v := c.View().Spec; !v.Bode || !strings.Contains(v.Caption, "ref C2, DUT C1") {
		t.Fatalf("sweeping, ref C2 DUT C1: %q", v.Caption)
	}
	r.eng.pts = []engine.BodePoint{{FreqHz: 1e3}, {FreqHz: 1e4}}
	if v := c.View().Spec; !strings.Contains(v.Caption, "ref C2, DUT C1") {
		t.Fatalf("2 points, ref C2 DUT C1: %q", v.Caption)
	}
}
