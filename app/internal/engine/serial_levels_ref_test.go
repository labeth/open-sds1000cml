// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package engine

import "github.com/labeth/open-dso-core/sample"

// learnSerialLevels keeps each channel's decoder threshold from a capture
// taken at scale sc, and the SPI clock rate when an SPI request is armed. A
// level that moved less than two codes is kept, so the plan does not change
// (and re-arm) on every capture's noise.
// TRLC-LINKS: REQ-SDS-013
func (e *Engine) learnSerialLevels(f *sample.Frame, sc [2]chScale) {
	e.learnSerialLevelsOf(f, sc, nil)
}
