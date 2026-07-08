package web

import (
	"math"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"open-sds/app/internal/engine"
)

func TestPerfServe(t *testing.T) {
	if os.Getenv("PERFSERVE") == "" {
		t.Skip("dev harness")
	}
	var seq atomic.Int64
	gen := func() *engine.Frame {
		n := seq.Add(1)
		const N = 2048
		c1 := make([]uint8, N)
		c2 := make([]uint8, N)
		for i := 0; i < N; i++ {
			c1[i] = uint8(128 + 90*math.Sin(2*math.Pi*7*float64(i)/N+float64(n)*0.05))
			c2[i] = uint8(128 + 60*math.Sin(2*math.Pi*13*float64(i)/N))
		}
		return &engine.Frame{C1: c1, C2: c2, Seq: uint64(n), Valid: N, WinCols: N,
			EdgeX: N / 2, TdivS: 500e-6, DisplayedS: 500e-6, SampleS: 800e-9, Trigd: true}
	}
	fs := &fakeScope{frameGen: gen, stats: engine.Stats{Running: true, TrigPosFrac: 0.5}}
	srv := httptest.NewServer(New(fs, nil, nil, nil).Handler())
	defer srv.Close()
	os.WriteFile("/tmp/perfserve.url", []byte(srv.URL), 0o644)
	time.Sleep(120 * time.Second)
}
