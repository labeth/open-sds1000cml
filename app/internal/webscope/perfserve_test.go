// ENGMODEL-OWNER-UNIT: FU-APP-WEB

package webscope

import (
	"math"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/labeth/open-dso-core/sample"
	engine "github.com/labeth/open-sds1000cml/app/internal/scopeapi"
)

// TRLC-LINKS: REQ-SDS-180
func TestPerfServe(t *testing.T) {
	if os.Getenv("PERFSERVE") == "" {
		t.Skip("dev harness")
	}
	var seq atomic.Int64
	gen := func() *sample.Frame {
		n := seq.Add(1)
		const N = 2048
		c1 := make([]uint8, N)
		c2 := make([]uint8, N)
		for i := range N {
			c1[i] = uint8(128 + 90*math.Sin(2*math.Pi*7*float64(i)/N+float64(n)*0.05))
			c2[i] = uint8(128 + 60*math.Sin(2*math.Pi*13*float64(i)/N))
		}
		return &sample.Frame{
			Seq:       uint64(n),
			WinPoints: N,
			TriggerAt: N / 2,
			SampleS:   800e-9,
			Snapshot:  sample.Settings{TdivS: 500e-6, DisplayedS: 500e-6},
			Status:    sample.Status{Triggered: true},
			Traces:    []sample.Trace{{Codes8: c1, Valid: N}, {Ch: 1, Codes8: c2, Valid: N}},
		}
	}
	fs := &fakeScope{frameGen: gen, stats: engine.Stats{Running: true, TrigPosFrac: 0.5}}
	srv := httptest.NewServer(newServer(fs, nil, nil, nil).Handler())
	defer srv.Close()
	if err := os.WriteFile("/tmp/perfserve.url", []byte(srv.URL), 0o600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(120 * time.Second)
}
