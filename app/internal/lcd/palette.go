// ENGMODEL-OWNER-UNIT: FU-APP-LCD

package lcd

import "github.com/labeth/open-dso-core/design"

// The LCD palette: each colour is a token of the shared palette
// (open-dso-core/design), so the LCD and the web UI cannot diverge.
var (
	colAxis   = tok("axis")
	colBG     = tok("screen")
	colBoxBg  = tok("lcd-box")
	colC1     = tok("c1")
	colC2     = tok("c2")
	colCursor = tok("cursor")
	colDim    = tok("dim")
	colGrid   = tok("grid")
	colInfo   = tok("text")
	colMath   = tok("math")
	colMenuBg = tok("lcd-menu")
	colOK     = tok("run")
	colSR     = tok("sr")
	colStale  = tok("stop")
	colTrig   = tok("trigger")
	colWarn   = tok("warn")
)

// tok is the shared palette's colour name in the LCD's RGB565. A name the
// palette lacks is a programming error, caught at start and by the tests.
// TRLC-LINKS: REQ-SDS-021
func tok(name string) uint16 {
	c, ok := design.Tokens[name]
	if !ok {
		panic("lcd: palette has no token " + name)
	}
	return rgb(c.R, c.G, c.B)
}
