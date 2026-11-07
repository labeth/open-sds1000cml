// ENGMODEL-OWNER-UNIT: FU-APP-PANEL

package panel

import (
	"fmt"
	"math"
)

// vstep is one V/div step of a channel's ladder at the BNC: its V/div, the
// analog range its codes are on (the coarser range a display zoom
// magnifies) and that zoom.
type vstep struct {
	vdiv, rng float64
	zoom      int
}

// defaultVdivV is Default setup's V/div at the BNC.
const defaultVdivV = 1.0

// noFrontEnd is the vertical step the panel assumes without a front end:
// 1 V/div.
var noFrontEnd = vstep{vdiv: 1, rng: 1, zoom: 1}

// ladder is channel ch's V/div ladder at the BNC: the front end's ladder
// (at the probe tip) over the probe, one step of 1 V/div without one.
// TRLC-LINKS: REQ-SDS-212
func (c *Controller) ladder(ch int) []vstep {
	if c.fe == nil {
		return []vstep{noFrontEnd}
	}
	p := c.fe.ProbeFactor(ch)
	steps := c.fe.Ladder(ch)
	out := make([]vstep, len(steps))
	for i, s := range steps {
		v := s.VoltsDiv / p
		out[i] = vstep{vdiv: v, rng: v * s.Zoom, zoom: int(s.Zoom)}
	}
	return out
}

// vpos is channel ch's place on its ladder.
// TRLC-LINKS: REQ-SDS-212
func (c *Controller) vpos(ch int) int {
	if c.fe == nil {
		return 0
	}
	return ladderPos(c.ladder(ch), c.fe.VdivV(ch))
}

// ladderPos is the step of l at V/div v, the first one when none is.
// TRLC-LINKS: REQ-SDS-212
func ladderPos(l []vstep, v float64) int {
	for i, s := range l {
		if s.vdiv == v {
			return i
		}
	}
	return 0
}

// vstepOf is channel ch's V/div step now.
// TRLC-LINKS: REQ-SDS-212
func (c *Controller) vstepOf(ch int) vstep {
	if c.fe == nil {
		return noFrontEnd
	}
	v, r := c.fe.VdivV(ch), c.fe.RangeV(ch)
	return vstep{vdiv: v, rng: r, zoom: int(math.Round(r / v))}
}

// setVpos puts channel ch on step pos of its ladder.
// TRLC-LINKS: REQ-SDS-212
func (c *Controller) setVpos(ch, pos int) error {
	l := c.ladder(ch)
	if pos < 0 || pos >= len(l) {
		return fmt.Errorf("panel: no V/div step %d on C%d's ladder", pos, ch+1)
	}
	return c.fe.SetVdivV(ch, l[pos].vdiv)
}

// vposs is both channels' places on their ladders.
// TRLC-LINKS: REQ-SDS-212
func (c *Controller) vposs() [2]int { return [2]int{c.vpos(0), c.vpos(1)} }

// tipLadder is channel ch's V/div ladder at the probe tip; 1 V/div alone
// without a front end.
// TRLC-LINKS: REQ-SDS-212
func (c *Controller) tipLadder(ch int) []float64 {
	if c.fe == nil {
		return []float64{noFrontEnd.vdiv}
	}
	steps := c.fe.Ladder(ch)
	out := make([]float64, len(steps))
	for i, s := range steps {
		out[i] = s.VoltsDiv
	}
	return out
}
