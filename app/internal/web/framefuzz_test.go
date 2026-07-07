package web

import (
	"math"
	"math/rand"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"open-sds/app/internal/engine"
)

// Frame-serving fuzz: even if an engine invariant slipped (Valid > len,
// absurd WinCols, NaN/negative EdgeX, mismatched env metadata), the web serve
// path — buildReply + measurement + window/deepWindow + binary encode — must
// not panic. A serving panic on ONE bad frame crashes the whole scope UI.
func TestFrameServeFuzz(t *testing.T) {
	rng := rand.New(rand.NewSource(0xF00D))
	var cur atomic.Pointer[engine.Frame]

	mkFrame := func(seq uint64) *engine.Frame {
		n := rng.Intn(6000)
		f := &engine.Frame{
			Seq: seq, Valid: n, SampleS: []float64{0, 800e-9, 2e-9, 1e-12}[rng.Intn(4)],
			WinCols: []int{0, 1, 2048, 4096, 100000, -5}[rng.Intn(6)],
			EdgeX:   []float64{-1, 0, float64(n) / 2, float64(n) * 2, math.NaN(), -1e9, 1e9}[rng.Intn(7)],
			TdivS:   500e-6, DisplayedS: 500e-6, Trigd: rng.Intn(2) == 0, Coherent: rng.Intn(2) == 0,
			Ptp: rng.Intn(300), Interp: rng.Intn(2) == 0,
			IsEnv: rng.Intn(4) == 0, EnvCols: rng.Intn(900),
		}
		clen := n
		if rng.Intn(5) == 0 { // sometimes claim more valid than we allocate
			clen = n / 2
		}
		if clen < 0 {
			clen = 0
		}
		f.C1 = make([]uint8, clen)
		f.C2 = make([]uint8, clen)
		for i := range f.C1 {
			f.C1[i] = uint8(rng.Intn(256))
			f.C2[i] = uint8(rng.Intn(256))
		}
		if f.IsEnv {
			ec := f.EnvCols
			if rng.Intn(3) == 0 {
				ec = 0 // env metadata lies: cols>0 but arrays nil/short
			}
			f.EnvMin = make([]uint8, ec)
			f.EnvMax = make([]uint8, ec)
			f.EnvMin2 = make([]uint8, ec)
			f.EnvMax2 = make([]uint8, ec)
		}
		// keep Valid within C1 for the copy-heavy paths half the time, to
		// exercise both the guarded and the invariant-violating branches
		if rng.Intn(2) == 0 && f.Valid > len(f.C1) {
			f.Valid = len(f.C1)
		}
		return f
	}

	fs := &fakeScope{
		frameGen: func() *engine.Frame { return cur.Load() },
		stats:    engine.Stats{Running: true, TrigPosFrac: 0.5, WinCols: 2048},
	}
	srv := httptest.NewServer(New(fs, nil, nil, nil).Handler())
	defer srv.Close()
	client := srv.Client()

	paths := []string{
		"/api/frame", "/api/frame?raw=1",
		"/api/frame.bin", "/api/frame.bin?raw=1",
		"/api/frame.bin?since=0&waitms=0", "/api/frame.bin?depth=1",
	}
	for i := 0; i < 1500; i++ {
		cur.Store(mkFrame(uint64(i + 1)))
		p := paths[rng.Intn(len(paths))]
		r, err := client.Get(srv.URL + p)
		if err != nil {
			t.Fatalf("iter %d GET %s: transport error %v (server crashed?)", i, p, err)
		}
		r.Body.Close()
		if r.StatusCode >= 500 {
			t.Fatalf("iter %d GET %s: status %d", i, p, r.StatusCode)
		}
	}
	// server still healthy
	r, err := client.Get(srv.URL + "/api/status")
	if err != nil || r.StatusCode != 200 {
		t.Fatalf("server unhealthy after frame fuzz: %v", err)
	}
	r.Body.Close()
}
