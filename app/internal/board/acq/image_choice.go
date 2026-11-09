// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package acq

import (
	"github.com/labeth/open-dso-core/hal"
)

// Choose is the image trigger t runs on (hal.ImageChooser), as the
// engine's image switch picks it: a bus request on its protocol image
// (ProtocolImage: the packet, line, frame, qualifier or fast image) once
// its threshold is known; a pulse, slope or video trigger, or the zones,
// on the qualifier image; an edge on the general image, or, kept, on the
// qualifier image (AUTO in BUS between the qualifier's idle triggers) or
// the fast image (an SPI clock measured again) while one of them is
// loaded. An image the build lacks is never chosen: the general image
// serves what it can. ANY.
// TRLC-LINKS: REQ-SDS-005, REQ-SDS-013, REQ-SDS-223
func (i *imagesFacet) Choose(t hal.TriggerSpec, keep bool) []string {
	b := (*Board)(i)
	return []string{FeatureOf(b.chooseImage(t, keep))}
}

// chooseImage is Choose's image.
// TRLC-LINKS: REQ-SDS-005, REQ-SDS-013
func (b *Board) chooseImage(t hal.TriggerSpec, keep bool) string {
	have := func(image string) string {
		if b.HasImage(image) {
			return image
		}
		return ImageGeneral
	}
	if len(t.Stages) == 0 {
		return ImageGeneral
	}
	g := &t.Stages[0]
	if bus, ok := busOf(g.Proto); ok {
		p := bus.Request
		in := b.trigInputs()
		p.Threshold, p.HaveThr = in.scale[p.ChA&1].Word(bus.Levels[0]), true
		if name := ProtocolImage(p); name != "" && (name == ImageFast || SerialThresholdOK(&p)) {
			return have(name)
		}
		return ImageGeneral
	}
	switch {
	case g.Node == hal.NodePulse || g.Node == hal.NodeSlope || g.Node == hal.NodeVideo || len(g.Zones) != 0:
		return have(ImageQualify)
	case keep && (b.imageLoaded == ImageQualify || b.imageLoaded == ImageFast):
		return b.imageLoaded
	}
	return ImageGeneral
}

var _ hal.ImageChooser = (*imagesFacet)(nil)
