// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package acq

import (
	"context"
	"slices"
	"time"

	"github.com/labeth/open-dso-core/hal"
)

// ImageSwitcher reconfigures the acquisition FPGA between its images: the
// general image and the stacking, protocol, qualifier, stream and fast
// images. All share the SRAM capture ABI.
// TRLC-LINKS: REQ-SDS-141, REQ-SDS-005
type ImageSwitcher interface {
	LoadStack() error
	LoadGeneral() error
	// LoadPacket and LoadLine load the protocol images of
	// ADR-IMAGE-REGROUP-DECIMATION.
	LoadPacket() error
	LoadLine() error
	HasStack() bool
	HasPacket() bool
	HasLine() bool
	// LoadFrame loads the MIL-1553 and SENT image (ADR-TRIGGER-ANY-TIMEBASE).
	LoadFrame() error
	HasFrame() bool
	// LoadStream loads the continuous-capture image roll mode runs on
	// (ADR-STREAM-IMAGE).
	LoadStream() error
	HasStream() bool
	// LoadQualify loads the image with the pulse, slope and video triggers
	// (ADR-HARDWARE-QUALIFIERS).
	LoadQualify() error
	HasQualify() bool
	// LoadFast loads the image with SPI on the samples (ADR-SAMPLE-DOMAIN-SPI).
	LoadFast() error
	HasFast() bool
}

// BitsImages is an ImageSwitcher with the digital stream image
// (ADR-STREAM-BITS).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-035
type BitsImages interface {
	LoadBits() error
	HasBits() bool
}

// The general and stacking images' names beside the protocol images'
// (ImagePacket, ImageQualify, ...): "" is the general image.
const (
	ImageGeneral = ""
	ImageStack   = "stack"
)

// imageFeatures is each image's feature, as Images names it.
var imageFeatures = []struct{ image, feature string }{
	{ImageGeneral, "general"},
	{ImageQualify, "qualify"},
	{ImageFast, "fast-spi"},
	{ImagePacket, "packet-trigger"},
	{ImageLine, "line-trigger"},
	{ImageFrame, "frame-trigger"},
	{ImageStack, "stack"},
	{ImageStream, "stream"},
	{ImageBits, "bits-stream"},
}

// imageReload is what a switch stalls acquisition for: the configuration
// over CS3 and the converters' restart.
const imageReload = 1300 * time.Millisecond

// TRLC-LINKS: REQ-SDS-005
func imageName(image string) string {
	if image == ImageGeneral {
		return ImageGeneralName
	}
	return image
}

// FeatureOf is image's feature name; "" for an unknown image.
// TRLC-LINKS: REQ-SDS-005
func FeatureOf(image string) string {
	for _, f := range imageFeatures {
		if f.image == image {
			return f.feature
		}
	}
	return ""
}

// SetImages attaches the image loader.
// TRLC-LINKS: REQ-SDS-005
func (b *Board) SetImages(s ImageSwitcher) { b.images = s }

// ImageLoader is the loader the board holds, nil without one.
// TRLC-LINKS: REQ-SDS-005
func (b *Board) ImageLoader() ImageSwitcher { return b.images }

// HasImage reports whether image is built in (the general image always
// is). ANY.
// TRLC-LINKS: REQ-SDS-005
func (b *Board) HasImage(image string) bool {
	if b.images == nil {
		return image == ImageGeneral
	}
	switch image {
	case ImageGeneral:
		return true
	case ImageStack:
		return b.images.HasStack()
	case ImagePacket:
		return b.images.HasPacket()
	case ImageLine:
		return b.images.HasLine()
	case ImageFrame:
		return b.images.HasFrame()
	case ImageStream:
		return b.images.HasStream()
	case ImageBits:
		bi, ok := b.images.(BitsImages)
		return ok && bi.HasBits()
	case ImageQualify:
		return b.images.HasQualify()
	case ImageFast:
		return b.images.HasFast()
	}
	return false
}

// loader is image's load.
// TRLC-LINKS: REQ-SDS-005
func (b *Board) loader(image string) func() error {
	switch image {
	case ImageStack:
		return b.images.LoadStack
	case ImagePacket:
		return b.images.LoadPacket
	case ImageLine:
		return b.images.LoadLine
	case ImageFrame:
		return b.images.LoadFrame
	case ImageStream:
		return b.images.LoadStream
	case ImageBits:
		if bi, ok := b.images.(BitsImages); ok {
			return bi.LoadBits
		}
	case ImageQualify:
		return b.images.LoadQualify
	case ImageFast:
		return b.images.LoadFast
	}
	return b.images.LoadGeneral
}

// LoadImage loads image (the general one for ""). The fabric's record is
// gone afterwards. LONG.
// TRLC-LINKS: REQ-SDS-005
func (b *Board) LoadImage(image string) error {
	err := b.loader(image)()
	b.state = hal.Idle
	if err == nil {
		b.imageLoaded = image
	}
	return err
}

type imagesFacet Board

// Provides is every image built in.
// TRLC-LINKS: REQ-SDS-005
func (i *imagesFacet) Provides() []hal.ImageSet {
	b := (*Board)(i)
	var out []hal.ImageSet
	for _, f := range imageFeatures {
		if b.HasImage(f.image) {
			out = append(out, hal.ImageSet{Features: []string{f.feature}, Needs: []string{"sram"}, ReloadCost: imageReload})
		}
	}
	return out
}

// Current is the image in use.
// TRLC-LINKS: REQ-SDS-005
func (i *imagesFacet) Current() hal.ImageSet {
	b := (*Board)(i)
	return hal.ImageSet{Features: []string{FeatureOf(b.imageLoaded)}, Triggers: b.trigCaps(), Needs: []string{"sram"}, ReloadCost: imageReload}
}

// Load loads the image that has every feature asked: each image has one, so
// one feature (or none: the general image). The loader refuses an image the
// build lacks.
// TRLC-LINKS: REQ-SDS-005
func (i *imagesFacet) Load(_ context.Context, features []string) error {
	b := (*Board)(i)
	want := ImageGeneral
	switch {
	case len(features) > 1:
		return hal.Refuse("FPGA image", "no image has "+features[0]+" and "+features[1]+" together")
	case len(features) == 1:
		k := slices.IndexFunc(imageFeatures, func(f struct{ image, feature string }) bool { return f.feature == features[0] })
		if k < 0 {
			return hal.Refuse("FPGA image", "no image has "+features[0])
		}
		want = imageFeatures[k].image
	}
	return b.LoadImage(want)
}
