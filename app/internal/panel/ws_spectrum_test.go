// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"strings"
	"testing"

	"open-sds/app/internal/engine"
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
	if !v.All || v.AllN != len(measParams) || v.AllRows[0][1] != "1kHz" || v.AllRows[0][2] != "3kHz" {
		t.Fatalf("all values %d %+v", v.AllN, v.AllRows[:3])
	}
}
