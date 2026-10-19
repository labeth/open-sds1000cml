package main

import (
	"testing"

	"open-sds/app/internal/lcd"
	"open-sds/app/internal/panel/wsview"
)

// The SOUND view repaints when its pane changes (a new tuner reading, a
// meter, a cycle) and not otherwise: it is not a time-evolving view that
// bypasses the repaint cache, whose redraws took a fifth of the core and
// ran Listen dry. The Bode sweep and the spectrogram still are.
// TRLC-LINKS: REQ-SDS-168, REQ-SDS-273
func TestSoundViewRepaintsOnlyOnChange(t *testing.T) {
	pane := func(cents float64) *wsview.SoundPane {
		return &wsview.SoundPane{HasPitch: true, Note: "A4", Cents: cents, Hz: "440.00 Hz", Strip: []float32{1, 2},
			Meter: [2]wsview.SoundMeter{{On: true}}, Traces: [][]float32{{0, 1, 0}}}
	}
	h := lcd.HUD{ViewMode: lcdSoundView, UI: wsview.View{Sound: pane(3)}}
	if lcdForceRepaint(h) {
		t.Fatal("the SOUND view bypasses the repaint cache")
	}
	a := renderSigOf(nil, h, false)
	if h.UI.Sound = pane(3); renderSigOf(nil, h, false) != a {
		t.Fatal("the same SOUND pane rebuilt changed the render signature")
	}
	if h.UI.Sound = pane(4); renderSigOf(nil, h, false) == a {
		t.Fatal("a new tuner reading left the render signature unchanged")
	}
	for _, v := range []int{3, 4} {
		if !lcdForceRepaint(lcd.HUD{ViewMode: v}) {
			t.Fatalf("view %d (a sweep or a waterfall) uses the repaint cache", v)
		}
	}
}
