// ENGMODEL-OWNER-UNIT: FU-APP-PANEL

package panel

import (
	ui "github.com/labeth/open-dso-core/uimodel/panel"
	"github.com/labeth/open-sds1000cml/app/internal/board/acq"
	"github.com/labeth/open-sds1000cml/app/internal/buildinfo"
	"github.com/labeth/open-sds1000cml/app/internal/engine"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// Hardware is this scope's acquisition as the UI model plans with it: the
// SRAM record's plan at a timebase, the roll timebase, the live view's
// search and envelope sizes, which decoded streams run gap-free, and the
// firmware line. They are this board's record memory and images.
type Hardware struct{}

var _ ui.Hardware = Hardware{}

// Record is the SRAM record a capture at tdivS takes.
// TRLC-LINKS: REQ-SDS-214
func (Hardware) Record(tdivS float64) (float64, int) {
	p := acq.PlanSRAM(tdivS)
	return p.SampleS, int(p.Log)
}

// RollMinTdivS is the timebase the live view rolls from.
// TRLC-LINKS: REQ-SDS-214
func (Hardware) RollMinTdivS() float64 { return engine.RollMinTdiv }

// SearchSamples is the live view's samples while a bus search reads it.
// TRLC-LINKS: REQ-SDS-214
func (Hardware) SearchSamples() int { return engine.SearchLiveSamples }

// EnvelopePoints is a live envelope's points: 1024 (min, max) buckets of
// Q8.8 words.
// TRLC-LINKS: REQ-SDS-214
func (Hardware) EnvelopePoints() int { return 2 * sramcapture.EnvelopeBucketsQ8 }

// StreamMode is how the engine streams a protocol at a rate.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (Hardware) StreamMode(proto, baud int, overdrive bool) string {
	return engine.SoftStreamMode(proto, baud, overdrive)
}

// Product is the firmware's name, version and build.
// TRLC-LINKS: REQ-SDS-212, REQ-SDS-174
func (Hardware) Product() string {
	return "open-sds1000cml " + buildinfo.Version + "  (" + buildinfo.Commit + ")"
}
