package main

import (
	"math"
	"testing"

	"open-sds/app/internal/engine"
	"open-sds/app/internal/lcd"
)

// The scope's zoom leases its stretch of a live envelope at full detail in
// Y-T only: X-Y draws no zoomed stretch, and the reads cost frames.
// TRLC-LINKS: REQ-SDS-021
func TestScopeViewLeaseOnlyInYT(t *testing.T) {
	f := &engine.Frame{PeakDetect: true, Valid: 4096, WinCols: 1024}
	hud := lcd.HUD{Zoom: 10, ZoomOff: 0.01}
	a, b, ok := scopeViewLease(hud, f)
	if !ok || math.Abs(a-(0.04-0.05)) > 1e-12 || math.Abs(b-(0.04+0.05)) > 1e-12 {
		t.Fatalf("Y-T zoomed: lease %v %g..%g, want -0.01..0.09 screen widths", ok, a, b)
	}
	hud.ViewMode = 1 // X-Y
	if _, _, ok := scopeViewLease(hud, f); ok {
		t.Fatal("X-Y leased a zoomed stretch it does not draw")
	}
	hud.ViewMode, hud.Zoom = 0, 1
	if _, _, ok := scopeViewLease(hud, f); ok {
		t.Fatal("not zoomed, yet leased")
	}
}
