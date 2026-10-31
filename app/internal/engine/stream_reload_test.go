// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"slices"
	"testing"
)

// streamImages is packetImages with a stream image that records its loads.
type streamImages struct{ *packetImages }

// TRLC-LINKS: REQ-SDS-035
func (f streamImages) HasStream() bool { return true }

// TRLC-LINKS: REQ-SDS-035
func (f streamImages) LoadStream() error {
	f.loads = append(f.loads, "stream")
	f.b.image = "stream"
	return nil
}

// A stream that faults before its first word leaves the stream image
// faulting every new stream at once until the image is loaded again: the
// next stream of any kind (roll, decoded, audio) loads it first; a loss
// later in a stream does not.
// TRLC-LINKS: REQ-SDS-035
func TestStreamFaultAtStartReloadsTheImage(t *testing.T) {
	e, _, images := newPacketTestEngine(t)
	si := streamImages{images}
	e.images = si
	e.noteStreamLoss(123456)
	if err := e.freshStreamImage(imageStream); err != nil || slices.Contains(images.loads, "stream") {
		t.Fatalf("a loss mid-stream reloaded: %v %v", err, images.loads)
	}
	e.noteStreamLoss(0)
	if err := e.freshStreamImage(imageStream); err != nil || !slices.Contains(images.loads, "stream") || e.protocolImage != imageStream {
		t.Fatalf("a fault at the first word: %v loads %v image %q", err, images.loads, e.protocolImage)
	}
	n := len(images.loads)
	if err := e.freshStreamImage(imageStream); err != nil || len(images.loads) != n {
		t.Fatalf("reloaded twice: %v", images.loads)
	}
}

// bitsImages is streamImages with the digital stream image.
type bitsImages struct{ streamImages }

// TRLC-LINKS: REQ-SDS-035
func (f bitsImages) HasBits() bool { return true }

// TRLC-LINKS: REQ-SDS-035
func (f bitsImages) LoadBits() error {
	f.loads = append(f.loads, "bits")
	f.b.image = "bits"
	return nil
}

// A decoded stream of digital words that faulted at its first word reloads
// the digital stream image, not the stream image: the stream image's peak
// words decode as digital words to noise.
// TRLC-LINKS: REQ-SDS-035
func TestStreamFaultReloadsTheStreamsOwnImage(t *testing.T) {
	e, _, images := newPacketTestEngine(t)
	e.images = bitsImages{streamImages{images}}
	e.noteStreamLoss(0)
	if err := e.freshStreamImage(imageBits); err != nil || !slices.Equal(images.loads, []string{"bits"}) || e.protocolImage != imageBits {
		t.Fatalf("digital stream fault: %v loads %v image %q", err, images.loads, e.protocolImage)
	}
}
