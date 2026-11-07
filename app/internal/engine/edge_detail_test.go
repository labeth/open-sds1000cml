// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package engine

import (
	"testing"
	"time"

	core "github.com/labeth/open-dso-core/engine"
	"github.com/labeth/open-dso-core/sample"
)

// The edge windows are read at most every edgeDetailEvery and carried to the
// frames between reads (every frame took the rate from 28 to 16 a second); a
// capture under other settings or another scale reads them at once.
// TRLC-LINKS: REQ-SDS-021
func TestEdgeDetailRate(t *testing.T) {
	var d edgeDetail
	t0 := time.Unix(100, 0)
	k := edgeKey{scale: [2]core.ChScale{{Vdiv: 1}, {Vdiv: 1}}}
	if !d.due(k, t0) {
		t.Fatal("nothing read yet: due")
	}
	w := &sample.Frame{SampleS: 2e-9}
	d.store(k, t0, w, nil)
	if d.due(k, t0.Add(core.EdgeDetailEvery/2)) || d.edge != w {
		t.Fatal("read again within the interval, or the windows dropped")
	}
	if !d.due(k, t0.Add(core.EdgeDetailEvery)) {
		t.Fatal("not due after the interval")
	}
	k2 := k
	k2.scale[0].Vdiv = 2
	if !d.due(k2, t0.Add(core.EdgeDetailEvery/2)) || d.edge != nil {
		t.Fatal("another scale kept the old windows")
	}
}
