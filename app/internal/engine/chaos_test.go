package engine

import (
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
)

// Concurrency chaos: the engine loop runs frames while API goroutines hammer
// every externally-reachable setter and reader. Run under -race; the test
// also asserts the engine still publishes at the end (no deadlock/wedge).
func TestEngineConcurrencyChaos(t *testing.T) {
	fb := newFakeBus()
	e, _ := newTestEngine(t, fb)
	e.bringUp()

	stop := atomic.Bool{}
	var wg sync.WaitGroup

	// the "engine goroutine": frames + boundary band changes, dispatched by
	// band kind exactly like run() — 400 iterations, then stop the hammers.
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer stop.Store(true)
		last := KindDecimated
		for i := 0; i < 400; i++ {
			e.mu.Lock()
			if e.pendSet {
				e.band = e.pendBand
				e.pendSet = false
				e.syncBandStatsLocked()
			}
			norm := e.norm
			e.mu.Unlock()
			if k := e.band.Kind(); k != last {
				e.transition(norm, false)
				last = k
			}
			switch e.band.Kind() {
			case KindRoll:
				e.rollUpdate(norm)
			case KindEnvelope:
				e.envFrame(norm)
			default:
				e.oneFrame(norm)
			}
			e.Consume()
		}
	}()

	hammer := func(seed int64, fn func(r *rand.Rand)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := rand.New(rand.NewSource(seed))
			for !stop.Load() {
				fn(r)
			}
		}()
	}
	tdivs := []float64{100e-9, 1e-6, 50e-6, 500e-6, 5e-3, 100e-3}
	hammer(1, func(r *rand.Rand) { e.SetTdiv(tdivs[r.Intn(len(tdivs))]) })
	hammer(2, func(r *rand.Rand) {
		win := 100 + r.Intn(4000)
		lo := make([]uint8, win)
		hi := make([]uint8, win)
		for j := range hi {
			hi[j] = 255
		}
		e.SetMask(&Mask{Lo: lo, Hi: hi, WinCols: win, Ch: r.Intn(2)})
		if r.Intn(4) == 0 {
			e.SetMask(nil)
		}
	})
	hammer(3, func(r *rand.Rand) {
		e.SetZones([]Zone{{DtLoS: -1e-6, DtHiS: 1e-6, CodeLo: r.Intn(255), CodeHi: 255}})
		e.SetZoneMode(r.Intn(2))
		e.SetMaskMode(r.Intn(3))
	})
	hammer(4, func(r *rand.Rand) {
		_ = e.Snapshot()
		_ = e.MaskFails()
		_ = e.Zones()
		_ = e.MaskEnvelope()
	})
	hammer(5, func(r *rand.Rand) {
		e.SetNorm(r.Intn(2) == 0)
		e.SetRunning(true)
		e.SetTrigPosFrac(r.Float64())
		e.SetMemDepth(3000 + r.Intn(20000))
		e.SetAcqMode(r.Intn(4))
		e.SetAvgCount(4 << r.Intn(5))
		e.ClearMaskFails()
		e.SetHoldoff(r.Float64() * 1e-3)
	})

	wg.Wait()

	// the engine must still be able to produce a frame afterwards
	e.SetZoneMode(ZoneOff)
	e.SetMaskMode(MaskOff)
	e.SetNorm(false)
	e.mu.Lock()
	if e.pendSet {
		e.band = e.pendBand
		e.pendSet = false
		e.syncBandStatsLocked()
	}
	e.mu.Unlock()
	e.transition(false, false)
	if e.band.Kind() == KindDecimated || e.band.Kind() == KindNativeFast {
		e.oneFrame(false)
		if s := e.Snapshot(); s.Published == 0 {
			t.Fatal("engine wedged: nothing published after chaos")
		}
	}
}
