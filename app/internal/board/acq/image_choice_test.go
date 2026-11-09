// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package acq

import (
	"context"
	"testing"
	"time"

	core "github.com/labeth/open-dso-core/engine"
	"github.com/labeth/open-dso-core/hal"
	"github.com/labeth/open-dso-core/hal/prototrig"
)

// The image a trigger runs on is the one the engine's image switch picks:
// a bus on its protocol image, a qualifier or the zones on the qualifier
// image, an edge on the general image unless kept on the qualifier or the
// fast image.
// TRLC-LINKS: REQ-SDS-005, REQ-SDS-013, REQ-SDS-223
func TestImageChoice(t *testing.T) {
	b, _, _ := newDrumBoard(t)
	im, _ := b.Images()
	ch, ok := im.(hal.ImageChooser)
	if !ok {
		t.Fatal("the images choose nothing")
	}
	src := hal.Source{Kind: hal.Analog, Index: 0}
	spec := func(st hal.TrigStage) hal.TriggerSpec {
		st.Source = src
		return hal.TriggerSpec{Stages: []hal.TrigStage{st}}
	}
	bus := func(p core.SerialParams) hal.TriggerSpec {
		return spec(hal.TrigStage{Node: hal.NodeProtocol, Proto: &prototrig.Bus{Request: p, Levels: [2]float64{0.5, 0.5}}})
	}
	for _, c := range []struct {
		name string
		t    hal.TriggerSpec
		keep bool
		on   string
		want string
	}{
		{"edge", spec(hal.TrigStage{Node: hal.NodeEdge}), false, ImageGeneral, "general"},
		{"pulse", spec(hal.TrigStage{Node: hal.NodePulse}), false, ImageGeneral, "qualify"},
		{"zones", spec(hal.TrigStage{Node: hal.NodeEdge, Zones: []hal.Zone{{Source: src}}}), false, ImageGeneral, "qualify"},
		{"edge kept on the qualifier", spec(hal.TrigStage{Node: hal.NodeEdge}), true, ImageQualify, "qualify"},
		{"edge not kept", spec(hal.TrigStage{Node: hal.NodeEdge}), false, ImageQualify, "general"},
		{"edge kept on the fast image", spec(hal.TrigStage{Node: hal.NodeEdge}), true, ImageFast, "fast-spi"},
		{"edge kept off the packet image", spec(hal.TrigStage{Node: hal.NodeEdge}), true, ImagePacket, "general"},
		{"CAN", bus(core.SerialParams{Proto: core.SerCAN, Baud: 500000}), false, ImageGeneral, "packet-trigger"},
		{"CAN without a rate", bus(core.SerialParams{Proto: core.SerCAN}), false, ImageGeneral, "general"},
		{"Manchester", bus(core.SerialParams{Proto: core.SerManchester, Baud: 10000}), false, ImageGeneral, "line-trigger"},
		{"UART on the general image", bus(core.SerialParams{Proto: core.SerUART, Baud: 9600, Bits: 8}), false, ImageGeneral, "general"},
	} {
		b.imageLoaded = c.on
		if got := ch.Choose(c.t, c.keep); len(got) != 1 || got[0] != c.want {
			t.Errorf("%s: %v, want %s", c.name, got, c.want)
		}
	}
}

// The periodic converter check is due on the converters' clock and says
// whether its recovery loaded the image again.
// TRLC-LINKS: REQ-SDS-231
func TestConverterCheckOnTheCycle(t *testing.T) {
	b, _, _ := newDrumBoard(t)
	cc, ok := b.Platform().(hal.ConverterChecker)
	if !ok {
		t.Fatal("no converter check")
	}
	b.SetConverterCheckDue(time.Now().Add(time.Hour))
	if cc.CheckDue() {
		t.Fatal("due before its time")
	}
	b.SetConverterCheckDue(time.Time{})
	if !cc.CheckDue() {
		t.Fatal("not due")
	}
	if cc.CheckConverters(context.Background()) {
		t.Fatal("converters that convert reloaded the image")
	}
	if cc.CheckDue() {
		t.Fatal("due again at once after a check")
	}
}
