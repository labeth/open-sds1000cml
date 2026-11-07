// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package engine

import (
	core "github.com/labeth/open-dso-core/engine"
	"github.com/labeth/open-dso-core/sample"
	"github.com/labeth/open-sds1000cml/app/internal/board/acq"
)

// The engine publishes sample.Frame: two traces of 8-bit codes, with
// optional Q8.8 precision words beside them. The three arena frames are
// reused in place: every producer path must set or clear ALL metadata every
// frame (spec 01 §2: a stale flag renders wrong output from correct data).

// newFrameArena is the engine's arena: frames of two traces of capacity
// codes each, reset to the converters' format.
// TRLC-LINKS: REQ-SDS-007
func newFrameArena(capacity int) *core.Arena {
	return core.NewArena(func() *sample.Frame {
		f := &sample.Frame{Traces: make([]sample.Trace, 2)}
		for ch := range f.Traces {
			f.Traces[ch] = sample.Trace{
				Codes8: make([]uint8, capacity),
				Min8:   make([]uint8, 0, envDisplayCols), Max8: make([]uint8, 0, envDisplayCols),
			}
		}
		acq.ResetFrame(f)
		return f
	})
}
