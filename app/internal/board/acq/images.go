// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package acq

import (
	"context"
	"errors"
	"fmt"
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

// errNoLoader is a load without an image loader attached.
var errNoLoader = errors.New("no image switcher")

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

// imageCalled is image as a trigger waiting for its load says it
// ("loading the qualifier FPGA image"), as the engine's status says it.
// TRLC-LINKS: REQ-SDS-005, REQ-SDS-011
func imageCalled(image string) string {
	switch image {
	case ImageQualify:
		return "qualifier"
	case ImageBits:
		return "digital stream"
	}
	return imageName(image)
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
	if b.images == nil {
		return errNoLoader
	}
	err := b.loader(image)()
	b.state, b.stopped = hal.Idle, true // the fabric applies nothing staged before: the next Arm commits
	b.reloads++
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
			out = append(out, hal.ImageSet{Name: imageName(f.image), Called: imageCalled(f.image), Features: []string{f.feature}, Needs: []string{"sram"}, ReloadCost: imageReload})
		}
	}
	return out
}

// Current is the image in use.
// TRLC-LINKS: REQ-SDS-005
func (i *imagesFacet) Current() hal.ImageSet {
	b := (*Board)(i)
	return hal.ImageSet{Name: imageName(b.imageLoaded), Called: imageCalled(b.imageLoaded), Features: []string{FeatureOf(b.imageLoaded)}, Triggers: b.trigCaps(), Needs: []string{"sram"}, ReloadCost: imageReload}
}

// Load loads the image that has every feature asked: each image has one, so
// one feature (or none: the general image), as every load the core asks
// for is made (loadFresh: the fabric halted, the capabilities probed, the
// panel scan written again and the converters brought up). The loader
// refuses an image the build lacks. OWNER, LONG.
// TRLC-LINKS: REQ-SDS-005, REQ-SDS-231
func (i *imagesFacet) Load(_ context.Context, features []string) error {
	want, err := imageOf(features)
	if err != nil {
		return err
	}
	return (*Board)(i).loadFresh(want)
}

// imageOf is the image with the features asked.
// TRLC-LINKS: REQ-SDS-005
func imageOf(features []string) (string, error) {
	want := ImageGeneral
	switch {
	case len(features) > 1:
		return "", hal.Refuse("FPGA image", "no image has "+features[0]+" and "+features[1]+" together")
	case len(features) == 1:
		k := slices.IndexFunc(imageFeatures, func(f struct{ image, feature string }) bool { return f.feature == features[0] })
		if k < 0 {
			return "", hal.Refuse("FPGA image", "no image has "+features[0])
		}
		want = imageFeatures[k].image
	}
	return want, nil
}

// loadFresh loads image as the engine's image switch does: the fabric
// halted, the image configured, its capabilities probed, the panel scan's
// settings written again and the converters brought up on it. OWNER, LONG.
// TRLC-LINKS: REQ-SDS-005, REQ-SDS-231
func (b *Board) loadFresh(image string) error {
	if b.images == nil {
		return hal.Refuse("FPGA image", "no image loader is attached")
	}
	ctx, cancel := context.WithTimeout(context.Background(), haltTimeout)
	err := b.mem.Halt(ctx)
	cancel()
	b.beat()
	if err != nil {
		return err
	}
	if err := b.LoadImage(image); err != nil {
		return fmt.Errorf("the %s image: %w", imageName(image), err)
	}
	b.beat()
	b.reprobe()
	b.beat()
	b.StartConverters("loading the "+imageName(image)+" image", b.reloadImage)
	return nil
}

// reprobe reads the loaded image's capabilities and writes the panel scan
// settings again, as after every load.
// TRLC-LINKS: REQ-SDS-005, REQ-SDS-022
func (b *Board) reprobe() {
	b.ProbeImage(b.logf)
	if err := b.ApplyPanelScan(); err != nil {
		b.logf("engine: panel scan: %v", err)
	}
}

// reloadImage configures the loaded image again (a converter recovery).
// OWNER, LONG.
// TRLC-LINKS: REQ-SDS-005, REQ-SDS-231
func (b *Board) reloadImage() error {
	if b.images == nil {
		return errNoReload
	}
	if err := b.LoadImage(b.imageLoaded); err != nil {
		return err
	}
	b.beat()
	b.reprobe()
	return nil
}
