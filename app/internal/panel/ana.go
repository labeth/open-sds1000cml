// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"open-sds/app/internal/analysis"
	"open-sds/app/internal/decode"
)

// The workspace tools use the shared analysis package (packets, eye and
// jitter, spectral peaks, statistics, reference difference, repeats)
// through this file only, so its API is bound in one place.

// Packet is a decoded message.
type Packet = analysis.Packet

// PacketList holds the listed packets still while messages repeat.
type PacketList = analysis.PacketList

// Eye accumulates an eye diagram.
type Eye = analysis.Eye

// EyeResult is an eye's figures and density grid.
type EyeResult = analysis.EyeResult

// EyeBox is an eye opening.
type EyeBox = analysis.EyeBox

// Peak is a spectral line.
type Peak = analysis.Peak

// THD is a peak list's harmonic distortion.
type THD = analysis.THD

// Stats is a running statistic.
type Stats = analysis.Stats

// The eye grid's code window: the graticule's 8 divisions.
const (
	eyeCodeHi = analysis.EyeCodeHi
	eyeCodeLo = analysis.EyeCodeLo
)

// anaPackets groups a decode into packets.
// TRLC-LINKS: REQ-SDS-214
func anaPackets(r decode.Result, recordLen int) []Packet { return analysis.Packets(r, recordLen) }

// anaNewEye returns an empty eye.
// TRLC-LINKS: REQ-SDS-215
func anaNewEye(cols, rows int) *Eye { return analysis.NewEye(cols, rows) }

// anaPeaks finds the strongest spectral lines, in frequency order.
// TRLC-LINKS: REQ-SDS-216
func anaPeaks(db []float64, binHz float64, n int) []Peak { return analysis.Peaks(db, binHz, n) }

// anaHarmonics numbers the harmonics of f0 (0: the largest peak) in place
// and returns the THD.
// TRLC-LINKS: REQ-SDS-216
func anaHarmonics(ps []Peak, f0 float64) THD { return analysis.Harmonics(ps, f0) }

// anaRefDiff compares a live trace with a reference (divisions).
// TRLC-LINKS: REQ-SDS-218
func anaRefDiff(live, ref []float64, tolDiv float64) (maxAbs, rms, pct float64) {
	return analysis.RefDiff(live, ref, tolDiv)
}

// anaRepeats reports whether cur repeats prev (the mask build's rule).
// TRLC-LINKS: REQ-SDS-218
func anaRepeats(prev, cur []float64, tolDiv float64, slack int) bool {
	return analysis.Repeats(prev, cur, tolDiv, slack)
}
