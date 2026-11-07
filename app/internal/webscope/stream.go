// ENGMODEL-OWNER-UNIT: FU-APP-WEB

package webscope

import (
	"github.com/labeth/open-dso-core/streamwire"
)

// stream is the panel's decoded stream (ADR-PANEL-DECODED-STREAM): its
// transcript as data, in the compact binary line format (decodedlines) and
// as the LCD's list writes it. A part the panel lacks answers as no stream.
type stream struct{ p Panel }

// StreamLines is the transcript from an absolute line index.
// TRLC-LINKS: REQ-SDS-018
func (st stream) StreamLines(from, limit int) (streamwire.LinesPage, bool) {
	src, ok := st.p.(interface {
		StreamLines(from, limit int) (streamwire.LinesPage, bool)
	})
	if !ok {
		return streamwire.LinesPage{}, false
	}
	return src.StreamLines(from, limit)
}

// StreamLinesBinary is the transcript from a line in the binary format.
// TRLC-LINKS: REQ-SDS-018
func (st stream) StreamLinesBinary(from int, dst []byte) (int, bool) {
	src, ok := st.p.(interface {
		StreamLinesBinary(from int, dst []byte) (int, bool)
	})
	if !ok {
		return 0, false
	}
	return src.StreamLinesBinary(from, dst)
}

// StreamWindow is the newest rows as the LCD's list writes them.
// TRLC-LINKS: REQ-SDS-018
func (st stream) StreamWindow(rows int) (streamwire.Page, bool) {
	src, ok := st.p.(interface {
		StreamWindow(rows int) (streamwire.Page, bool)
	})
	if !ok {
		return streamwire.Page{}, false
	}
	return src.StreamWindow(rows)
}
