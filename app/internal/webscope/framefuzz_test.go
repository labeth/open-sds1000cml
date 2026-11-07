// ENGMODEL-OWNER-UNIT: FU-APP-WEB

package webscope

import (
	"math"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/labeth/open-dso-core/sample"
	"github.com/labeth/open-sds1000cml/app/internal/engine"
)

// Frame-serving fuzz: even if an engine invariant slipped (Valid > len,
// absurd WinCols, NaN/negative EdgeX, mismatched env metadata), the web serve
// path — buildReply + measurement + window + binary encode — must
// not panic. A serving panic on ONE bad frame crashes the whole scope UI.
// TRLC-LINKS: REQ-SDS-163
func TestFrameServeFuzz(t *testing.T) {
	rng := rand.New(rand.NewSource(0xF00D))
	var cur atomic.Pointer[sample.Frame]

	mkFrame := func(seq uint64) *sample.Frame {
		n := rng.Intn(6000)
		f := &sample.Frame{
			Seq: seq, Traces: []sample.Trace{{Valid: n}, {Ch: 1, Valid: n}},
			SampleS:   []float64{0, 800e-9, 2e-9, 1e-12}[rng.Intn(4)],
			WinPoints: []int{0, 1, 2048, 4096, 100000, -5}[rng.Intn(6)],
			TriggerAt: []float64{-1, 0, float64(n) / 2, float64(n) * 2, math.NaN(), -1e9, 1e9}[rng.Intn(7)],
			Snapshot:  sample.Settings{TdivS: 500e-6, DisplayedS: 500e-6},
			Status:    sample.Status{Triggered: rng.Intn(2) == 0, Coherent: rng.Intn(2) == 0},
		}
		f.SourcePtp = rng.Intn(300)
		f.Flags.Set(sample.Interp, rng.Intn(2) == 0)
		env, envCols := rng.Intn(4) == 0, rng.Intn(900)
		f.Flags.Set(sample.Envelope, env)
		clen := n
		if rng.Intn(5) == 0 { // sometimes claim more valid than we allocate
			clen = n / 2
		}
		if clen < 0 {
			clen = 0
		}
		f.Traces[0].Codes8 = make([]uint8, clen)
		f.Traces[1].Codes8 = make([]uint8, clen)
		for i := range f.Codes8(0) {
			f.Codes8(0)[i] = uint8(rng.Intn(256))
			f.Codes8(1)[i] = uint8(rng.Intn(256))
		}
		if env {
			ec := envCols
			if rng.Intn(3) == 0 {
				ec = 0 // env metadata lies: cols>0 but arrays nil/short
			}
			f.Traces[0].Min8 = make([]uint8, ec)
			f.Traces[0].Max8 = make([]uint8, ec)
			f.Traces[1].Min8 = make([]uint8, ec)
			f.Traces[1].Max8 = make([]uint8, ec)
		}
		// keep Valid within C1 for the copy-heavy paths half the time, to
		// exercise both the guarded and the invariant-violating branches
		if rng.Intn(2) == 0 && f.Len() > len(f.Codes8(0)) {
			f.SetLen(len(f.Codes8(0)))
		}
		return f
	}

	fs := &fakeScope{
		frameGen: cur.Load,
		stats:    engine.Stats{Running: true, TrigPosFrac: 0.5, WinCols: 2048},
	}
	srv := httptest.NewServer(newServer(fs, nil, nil, nil).Handler())
	defer srv.Close()
	client := srv.Client()

	paths := []string{
		"/api/frame.bin", "/api/frame.bin?raw=1",
		"/api/frame.bin?since=0&waitms=0", "/api/frame.bin?depth=1",
	}
	for i := range 1500 {
		cur.Store(mkFrame(uint64(i + 1)))
		p := paths[rng.Intn(len(paths))]
		r, err := httpGet(t, client, srv.URL+p)
		if err != nil {
			t.Fatalf("iter %d GET %s: transport error %v (server crashed?)", i, p, err)
		}
		_ = r.Body.Close()
		if r.StatusCode >= 500 {
			t.Fatalf("iter %d GET %s: status %d", i, p, r.StatusCode)
		}
	}
	// server still healthy
	r, err := httpGet(t, client, srv.URL+"/api/status")
	if err != nil || r.StatusCode != http.StatusOK {
		t.Fatalf("server unhealthy after frame fuzz: %v", err)
	}
	_ = r.Body.Close()
}
