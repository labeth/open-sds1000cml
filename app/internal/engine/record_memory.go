// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package engine

import (
	"context"
	"io"

	"github.com/labeth/open-dso-core/superres"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// recordMemory is what the engine asks of the SRAM capture fabric: arming and
// recalling records, streams, the decoded-event queue, the stacking port, the
// panel chain and the image's capabilities. *sramcapture.Capture implements
// it; naming the set lets a recorder stand in for the fabric and log every
// call the acquisition owner makes.
// TRLC-LINKS: REQ-SDS-033, REQ-SDS-082
type recordMemory interface {
	Arm(ctx context.Context, cfg sramcapture.Config) error
	Poll() (sramcapture.Metadata, error)
	Status() (sramcapture.Metadata, error)
	Force(ctx context.Context) error
	Halt(ctx context.Context) error
	Snapshot(ctx context.Context) ([10]uint8, error)
	RestartEncode() error
	Beats() uint64
	Locked(fn func())
	SetSpanHook(fn func(done, total int))

	Recall(ctx context.Context, offset, count uint32, dst io.Writer) (int64, error)
	RecallForward(ctx context.Context, offset, count uint32, dst io.Writer) (int64, error)
	RecallReadsOf(ctx context.Context, m sramcapture.Metadata, reads ...sramcapture.RecordRead) error
	EnvelopeDiagnostics() (int, string)

	SupportsUART() (bool, error)
	SupportsSampleUART() (bool, error)
	SupportsI2C() (bool, error)
	SupportsSampleI2C() (bool, error)
	SupportsSPI() (bool, error)
	SupportsSampleSPI() (bool, error)
	SupportsSENT() (bool, error)
	SupportsUSBLS() (bool, error)
	SupportsMIL1553() (bool, error)
	SupportsManchester() (bool, error)
	SupportsSequence() (bool, error)
	SequenceOnly() (bool, error)
	SupportsEnvelope() (bool, error)
	SupportsQualifier() (bool, error)
	SupportsSymbol() (bool, error)
	SupportsPanel() (bool, error)
	SupportsDecodedCapture() (bool, error)
	PacketProtocols() (uint16, error)
	RawOnly() (bool, error)
	BoxDecimation() (bool, error)
	PickDecimation() (bool, error)
	PickOnAsking() (bool, error)
	SymbolMatches() (uint16, error)

	StartStream(ctx context.Context, cfg sramcapture.Config, continuous, peak bool, pace sramcapture.StreamPace) error
	StartStreamMarked(ctx context.Context, cfg sramcapture.Config, m sramcapture.StreamMarkMode) error
	StartStreamRing(ctx context.Context, cfg sramcapture.Config, streamLog uint8, pace sramcapture.StreamPace) error
	DrainStream(ctx context.Context, expected uint64, dst io.Writer) (sramcapture.StreamBlock, error)
	StreamStatus() (sramcapture.StreamStatus, error)
	StopStream(ctx context.Context) error
	StreamMarksSupported() bool
	SetStreamMarks(ch int, level, hyst uint8, m sramcapture.StreamMarkMode) error
	StreamMarks() (sramcapture.StreamMarkRead, error)
	StreamRingSupported() bool
	FreezeRing(ctx context.Context) (sramcapture.Metadata, uint32, error)

	StartDecodedCapture(ctx context.Context, cfg sramcapture.Config) (*sramcapture.DecodedCapture, error)
	EnableDecodedEvents(enabled bool) (uint32, error)
	ReadDecodedEventsInto(epoch uint32, dst []sramcapture.DecodedEvent) ([]sramcapture.DecodedEvent, error)
	SetDecodedLineMode(proto int) (bool, error)
	ReadDecodedLines(epoch uint32, from int, flush bool, dst []byte) (int, bool, error)

	StackBegin(cfg sramcapture.StackConfig) (int, error)
	StackResetTile(ctx context.Context) error
	StackScan(ctx context.Context, first, n int, initial uint32) (hits, crossings, rejected uint32, err error)
	StackDownload(ctx context.Context, dst []superres.FPGAStackMoments) error

	ConfigurePanel(p sramcapture.PanelConfig) error
	ReadPanel() (sramcapture.PanelFrame, error)
}

var _ recordMemory = (*sramcapture.Capture)(nil)
