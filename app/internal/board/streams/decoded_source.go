// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package streams

import (
	"context"

	"github.com/labeth/open-dso-core/decodedlines"
	"github.com/labeth/open-dso-core/streamview"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// streamSource is the decoded streams' captures as the core's decoded
// stream reads them: the record identity and its metadata in the stream's
// terms.
type streamSource struct{ e *DecodedStreams }

// StreamSource is the decoded streams as the core's decoded stream (BUS's
// Stream view, streamview.Controller) follows them.
// TRLC-LINKS: REQ-SDS-018
func (d *DecodedStreams) StreamSource() streamview.Source { return streamSource{d} }

var _ streamview.Source = streamSource{}

// statusOf is the stream's view of a record's metadata.
// TRLC-LINKS: REQ-SDS-018
func statusOf(m sramcapture.Metadata) streamview.Status {
	return streamview.Status{
		Triggered: m.Triggered, Frozen: m.Frozen, Ready: m.Ready, HasSampleTimeline: m.HasSampleTimeline,
		SampleFirst: m.SampleFirst, SampleLast: m.SampleLast, TriggerIndex: m.TriggerIndex, Length: m.Length,
	}
}

// BeginDecodedStream starts the engine's decoded stream.
// TRLC-LINKS: REQ-SDS-018
func (s streamSource) BeginDecodedStream(ctx context.Context, p SerialParams, trig StreamTrigger, value int) (streamview.ID, error) {
	id, err := s.e.BeginDecodedStream(ctx, p, trig, value)
	return streamview.ID(id), err
}

// ReadDecodedBatchInto reads the stream's queued events.
// TRLC-LINKS: REQ-SDS-018
func (s streamSource) ReadDecodedBatchInto(ctx context.Context, id streamview.ID, dst []decodedlines.Event) (int, error) {
	return s.e.ReadDecodedBatchInto(ctx, sramcapture.RecordIdentity(id), dst)
}

// DecodedCaptureStatus is the capture's trigger and record.
// TRLC-LINKS: REQ-SDS-018
func (s streamSource) DecodedCaptureStatus(ctx context.Context, id streamview.ID) (streamview.Status, error) {
	m, err := s.e.DecodedCaptureStatus(ctx, sramcapture.RecordIdentity(id))
	return statusOf(m), err
}

// FreezeDecodedStream ends the capture's acquisition.
// TRLC-LINKS: REQ-SDS-018
func (s streamSource) FreezeDecodedStream(ctx context.Context, id streamview.ID) (streamview.Status, bool, error) {
	m, adopted, err := s.e.FreezeDecodedStream(ctx, sramcapture.RecordIdentity(id))
	return statusOf(m), adopted, err
}

// EndDecodedCapture tears the capture down.
// TRLC-LINKS: REQ-SDS-018
func (s streamSource) EndDecodedCapture(ctx context.Context, id streamview.ID) error {
	return s.e.EndDecodedCapture(ctx, sramcapture.RecordIdentity(id))
}

// ReadDecodedLines pulls the worker's lines; ok false means event mode.
// TRLC-LINKS: REQ-SDS-018
func (s streamSource) ReadDecodedLines(ctx context.Context, id streamview.ID, from int, flush bool) (decodedlines.Batch, bool, error) {
	return s.e.ReadDecodedLines(ctx, sramcapture.RecordIdentity(id), from, flush)
}

// DiscardDecodedRecord drops the record a stopped stream would hand the
// display.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-228
func (s streamSource) DiscardDecodedRecord() { s.e.DiscardDecodedRecord() }

// DecodedStreamMode says how the stream runs, for the displays.
// TRLC-LINKS: REQ-SDS-018
func (s streamSource) DecodedStreamMode() string { return s.e.DecodedStreamMode() }
