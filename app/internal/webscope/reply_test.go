// ENGMODEL-OWNER-UNIT: FU-APP-WEB

package webscope

import (
	"github.com/labeth/open-dso-core/sample"
	"github.com/labeth/open-dso-core/scopeweb"
	"github.com/labeth/open-dso-core/webserver/wire"
)

// buildReply is the web scope's v1 reply of frame f, as the tests ask it.
func (s *Scope) buildReply(f *sample.Frame, cols int, full bool, since uint64, off, vpc [2]float64, posFrac float64, measThrottle bool) wire.FrameReply {
	return s.BuildReply(f, scopeweb.ReplyQuery{Cols: cols, Full: full, Since: since, Off: off, Vpc: vpc, PosFrac: posFrac, MeasThrottle: measThrottle})
}
