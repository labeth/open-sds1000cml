// ENGMODEL-OWNER-UNIT: FU-APP-LCD

package lcd

import "github.com/labeth/open-dso-core/render"

// Screen is the panel's layout table: the renderer's 800×480 metrics, whose
// F-key rows were measured against this bezel.
var Screen = &render.Metrics800x480

// NewBackBuffer returns a blank page for the panel: the back buffer Present
// copies to /dev/fb0, and the persistence layer.
// TRLC-LINKS: REQ-SDS-021
func NewBackBuffer() *render.Image565 { return render.NewImage565(Screen) }
