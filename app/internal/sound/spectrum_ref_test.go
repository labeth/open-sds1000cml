// ENGMODEL-OWNER-UNIT: FU-APP-SOUND
package sound

import (
	"math"
	"strconv"
)

// FloorDBV is the scope's own floor in a bin of binHz at vpc volts a code:
// noise of density FloorCodesPerRtHz through a Hann bin (1.5 bins wide),
// read on the spectrum's sine-RMS scale.
// TRLC-LINKS: REQ-SDS-270
func FloorDBV(binHz, vpc float64) float64 {
	if !(binHz > 0) || !(vpc > 0) {
		return math.Inf(-1)
	}
	return 20 * math.Log10(FloorCodesPerRtHz*vpc*math.Sqrt(1.5*binHz))
}

// LogX places f on a log axis from f0 to f1 as 0..1.
// TRLC-LINKS: REQ-SDS-270
func LogX(f, f0, f1 float64) float64 {
	if !(f > 0) {
		return math.Inf(-1)
	}
	return math.Log(f/f0) / math.Log(f1/f0)
}

// NoteGrid is the C of every octave between f0 and f1 Hz (A4 = a4), for a
// log axis's note grid: frequencies and names ("C4").
// TRLC-LINKS: REQ-SDS-270
func NoteGrid(f0, f1, a4 float64) ([]float64, []string) {
	if !(a4 > 0) {
		a4 = 440
	}
	var fs []float64
	var ns []string
	for oct := -1; oct <= 10; oct++ {
		midi := 12 * (oct + 1)
		f := a4 * math.Pow(2, float64(midi-69)/12)
		if f >= f0 && f <= f1 {
			fs = append(fs, f)
			ns = append(ns, "C"+strconv.Itoa(oct))
		}
	}
	return fs, ns
}
