// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

// Package streams is the SDS1000CML+ board's decoded streams: the FPGA's
// decoded captures, the software-decoded streams of the symbol protocols
// with their history ring, and the client decode tap a web page decodes the
// digital stream through. The core's HAL cycle runs them as the board's
// stream sessions (hal.Session).
package streams

import (
	"errors"
	"sync/atomic"

	core "github.com/labeth/open-dso-core/engine"
	"github.com/labeth/open-dso-core/instrument"
	"github.com/labeth/open-sds1000cml/app/internal/board/acq"
)

type (
	// ClientChunk is the shared type used by decoded streams.
	ClientChunk = core.ClientChunk
	// ClientStreamInfo is the shared type used by decoded streams.
	ClientStreamInfo = core.ClientStreamInfo
	// ClientWords is the shared type used by decoded streams.
	ClientWords = core.ClientWords
	// Clock is the shared type used by decoded streams.
	Clock = core.Clock
	// PatternElem is the shared type used by decoded streams.
	PatternElem = core.PatternElem
	// SerialDecoder is the shared type used by decoded streams.
	SerialDecoder = core.SerialDecoder
	// SerialParams is the shared type used by decoded streams.
	SerialParams = core.SerialParams
	// Stats is the shared type used by decoded streams.
	Stats = instrument.Stats
	// InterleaveCalibration is the shared type used by decoded streams.
	InterleaveCalibration = acq.InterleaveCalibration
)

// ClientReplyMax is the most words a client decode read returns.
const ClientReplyMax = core.ClientReplyMax

// ErrStopped is returned when the streams' owner has stopped.
var ErrStopped = errors.New("engine: stopped")

// streamPumper is the bus worker's stream-bank pump (bus.Dev with a worker).
type streamPumper interface {
	SetIsolatedStreamPump(bool) (bool, error)
	ReadIsolatedStream([]byte) (uint64, int, bool, error)
}

// streamPeeker reads the worker's stream words in place in the ring it
// shares with the app (bus.Dev.PeekIsolatedStream).
type streamPeeker interface {
	PeekIsolatedStream(release int) (first uint64, a, b []byte, err error)
}

// NoteImages tells the decoded streams' planning (SoftStreamMode) whether
// the build has the digital stream image: the board's image loader s.
// TRLC-LINKS: REQ-SDS-005, REQ-SDS-018
func NoteImages(s acq.ImageSwitcher) {
	b, ok := s.(acq.BitsImages)
	bitsImage.Store(ok && b.HasBits())
}

// bitsImage reports the build's digital stream image to SoftStreamMode,
// which plans before any stream runs (the panel's decode menu asks it).
var bitsImage atomic.Bool
