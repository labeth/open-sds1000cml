// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package streams

import (
	"slices"
	"testing"

	"github.com/labeth/open-sds1000cml/app/internal/board/acq"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// loadRecorder is the board's image loader recording what it loads; it has
// the stream image, and the digital stream image when bits says so.
type loadRecorder struct {
	loads []string
	bits  bool
}

func (f *loadRecorder) load(name string) error { f.loads = append(f.loads, name); return nil }

// TRLC-LINKS: REQ-SDS-141
func (f *loadRecorder) LoadStack() error { return f.load("stack") }

// TRLC-LINKS: REQ-SDS-005
func (f *loadRecorder) LoadGeneral() error { return f.load("general") }

// TRLC-LINKS: REQ-SDS-013
func (f *loadRecorder) LoadPacket() error { return f.load("packet") }

// TRLC-LINKS: REQ-SDS-013
func (f *loadRecorder) LoadLine() error { return f.load("line") }

// TRLC-LINKS: REQ-SDS-013
func (f *loadRecorder) LoadFrame() error { return f.load("frame") }

// TRLC-LINKS: REQ-SDS-035
func (f *loadRecorder) LoadStream() error { return f.load("stream") }

// TRLC-LINKS: REQ-SDS-011
func (f *loadRecorder) LoadQualify() error { return f.load("qualify") }

// TRLC-LINKS: REQ-SDS-229
func (f *loadRecorder) LoadFast() error { return f.load("fast") }

// TRLC-LINKS: REQ-SDS-035
func (f *loadRecorder) LoadBits() error { return f.load("bits") }

// TRLC-LINKS: REQ-SDS-141
func (f *loadRecorder) HasStack() bool { return false }

// TRLC-LINKS: REQ-SDS-013
func (f *loadRecorder) HasPacket() bool { return true }

// TRLC-LINKS: REQ-SDS-013
func (f *loadRecorder) HasLine() bool { return true }

// TRLC-LINKS: REQ-SDS-013
func (f *loadRecorder) HasFrame() bool { return true }

// TRLC-LINKS: REQ-SDS-035
func (f *loadRecorder) HasStream() bool { return true }

// TRLC-LINKS: REQ-SDS-011
func (f *loadRecorder) HasQualify() bool { return true }

// TRLC-LINKS: REQ-SDS-229
func (f *loadRecorder) HasFast() bool { return true }

// TRLC-LINKS: REQ-SDS-035
func (f *loadRecorder) HasBits() bool { return f.bits }

// reloadStreams are the decoded streams over a board with the image loader
// images, its memory answering a halt.
func reloadStreams(t *testing.T, images *loadRecorder) (*DecodedStreams, *acq.Board) {
	t.Helper()
	capture, err := sramcapture.New(&haltBus{})
	if err != nil {
		t.Fatal(err)
	}
	b := acq.New(acq.Config{Memory: capture})
	b.SetImages(images)
	return NewBoardStreams(b, nil, t.Logf), b
}

// A stream that faults before its first word leaves the stream image
// faulting every new stream at once until the image is loaded again: the
// next stream of any kind (roll, decoded, audio) loads it first; a loss
// later in a stream does not.
// TRLC-LINKS: REQ-SDS-035
func TestStreamFaultAtStartReloadsTheImage(t *testing.T) {
	images := &loadRecorder{}
	e, b := reloadStreams(t, images)
	e.h.noteStreamLoss(123456)
	if err := e.h.freshStreamImage(acq.ImageStream); err != nil || slices.Contains(images.loads, "stream") {
		t.Fatalf("a loss mid-stream reloaded: %v %v", err, images.loads)
	}
	e.h.noteStreamLoss(0)
	if err := e.h.freshStreamImage(acq.ImageStream); err != nil || !slices.Contains(images.loads, "stream") || b.LoadedImage() != acq.ImageStream {
		t.Fatalf("a fault at the first word: %v loads %v image %q", err, images.loads, b.LoadedImage())
	}
	n := len(images.loads)
	if err := e.h.freshStreamImage(acq.ImageStream); err != nil || len(images.loads) != n {
		t.Fatalf("reloaded twice: %v", images.loads)
	}
}

// A decoded stream of digital words that faulted at its first word reloads
// the digital stream image, not the stream image: the stream image's peak
// words decode as digital words to noise.
// TRLC-LINKS: REQ-SDS-035
func TestStreamFaultReloadsTheStreamsOwnImage(t *testing.T) {
	images := &loadRecorder{bits: true}
	e, b := reloadStreams(t, images)
	e.h.noteStreamLoss(0)
	if err := e.h.freshStreamImage(acq.ImageBits); err != nil || !slices.Equal(images.loads, []string{"bits"}) || b.LoadedImage() != acq.ImageBits {
		t.Fatalf("digital stream fault: %v loads %v image %q", err, images.loads, b.LoadedImage())
	}
}
