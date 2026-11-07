// ENGMODEL-OWNER-UNIT: FU-APP-LCD

package lcd

import (
	"flag"
	"path/filepath"
	"testing"

	"github.com/labeth/open-dso-core/render"
	"github.com/labeth/open-dso-core/render/rendertest"
)

// updateGolden rewrites testdata/golden from the current render (commit the
// PNGs with the change that caused them):
//
//	go test ./internal/lcd -run Golden -update-golden
var updateGolden = flag.Bool("update-golden", false, "rewrite testdata/golden/*.png from the current render")

// The panel shows the core's golden scenes as the core renders them: drawn
// into the back buffer this package presents (an 800×480 RGB565 page) and
// read back as the web's screen view encodes it.
// TRLC-LINKS: REQ-SDS-021, REQ-SDS-211
func TestGolden(t *testing.T) {
	for _, s := range rendertest.Scenes() {
		t.Run(s.Name, func(t *testing.T) {
			back := render.NewImage565()
			if len(back.Pix) != pageBytes {
				t.Fatalf("back buffer %d bytes, the panel's page %d", len(back.Pix), pageBytes)
			}
			s.Draw(back)
			rendertest.Check(t, filepath.Join("testdata", "golden"), s.Name, render.EncodePNG(back), rendertest.MaxDiffPixels, *updateGolden)
		})
	}
}
