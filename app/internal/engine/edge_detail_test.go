// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"testing"
	"time"
)

// The edge after the trigger edge, the other way: past the trigger edge's
// own bucket, the first bucket that comes back from the trigger edge's new
// level; none on a flat envelope or before it settles.
// TRLC-LINKS: REQ-SDS-021
func TestOppositeEdgeBucket(t *testing.T) {
	// A square of 20-bucket periods: high from bucket 10 to 19, low 0-9.
	pairs := make([]uint8, 0, 200)
	for b := 0; b < 100; b++ {
		lo, hi := uint8(50), uint8(50)
		switch {
		case b%20 == 10: // a rising edge inside the bucket
			hi = 200
		case b%20 == 0 && b > 0: // a falling edge inside the bucket
			hi = 200
		case b%20 > 10:
			lo, hi = 200, 200
		}
		pairs = append(pairs, lo, hi)
	}
	if got := oppositeEdgeBucket(pairs, 10, true); got != 20 {
		t.Fatalf("falling edge after the rising trigger at bucket 10: %d", got)
	}
	if got := oppositeEdgeBucket(pairs, 20, false); got != 30 {
		t.Fatalf("rising edge after the falling trigger at bucket 20: %d", got)
	}
	flat := make([]uint8, 200)
	for i := range flat {
		flat[i] = 128
	}
	if got := oppositeEdgeBucket(flat, 10, true); got != -1 {
		t.Fatalf("flat envelope: %d", got)
	}
	if got := oppositeEdgeBucket(pairs, 90, true); got != -1 {
		t.Fatalf("no edge after the last one: %d", got)
	}
}

// The edge windows are read at most every edgeDetailEvery and carried to the
// frames between reads (every frame took the rate from 28 to 16 a second); a
// capture under other settings or another scale reads them at once.
// TRLC-LINKS: REQ-SDS-021
func TestEdgeDetailRate(t *testing.T) {
	var d edgeDetail
	t0 := time.Unix(100, 0)
	k := edgeKey{scale: [2]chScale{{vdiv: 1}, {vdiv: 1}}}
	if !d.due(k, t0) {
		t.Fatal("nothing read yet: due")
	}
	w := &EdgeWindow{SampleS: 2e-9}
	d.store(k, t0, w, nil)
	if d.due(k, t0.Add(edgeDetailEvery/2)) || d.edge != w {
		t.Fatal("read again within the interval, or the windows dropped")
	}
	if !d.due(k, t0.Add(edgeDetailEvery)) {
		t.Fatal("not due after the interval")
	}
	k2 := k
	k2.scale[0].vdiv = 2
	if !d.due(k2, t0.Add(edgeDetailEvery/2)) || d.edge != nil {
		t.Fatal("another scale kept the old windows")
	}
}

// A serial match ends in its stop bit, no edge: the edge windows go to the
// edge nearest the trigger point, either way, and the next one the other way
// (the owner's UART trigger read no rise, ever: the trigger window held the
// flat stop bit and the opposite one was sought in the edge trigger's sense).
// TRLC-LINKS: REQ-SDS-021
func TestNearestEdgeBucket(t *testing.T) {
	pairs := make([]uint8, 0, 200)
	for b := 0; b < 100; b++ { // low 0-29, a rise inside 30, high 31-59, a fall between 59 and 60
		lo, hi := uint8(50), uint8(50)
		switch {
		case b == 30:
			hi = 200
		case b > 30 && b < 60:
			lo, hi = 200, 200
		}
		pairs = append(pairs, lo, hi)
	}
	if b, rising := nearestEdgeBucket(pairs, 50); b != 60 || rising {
		t.Fatalf("from 50: edge %d rising %v, want the fall at 60", b, rising)
	}
	if b, rising := nearestEdgeBucket(pairs, 36); b != 30 || !rising {
		t.Fatalf("from 36: edge %d rising %v, want the rise at 30", b, rising)
	}
	if b, _ := nearestEdgeBucket(pairs, 50); oppositeEdgeBucket(pairs, b, false) != -1 {
		t.Fatal("no rise after the fall at 60 in this record")
	}
	if b, rising := nearestEdgeBucket(pairs, 25); b != 30 || !rising {
		t.Fatalf("from 25: edge %d rising %v", b, rising)
	}
	flat := make([]uint8, 200)
	if b, _ := nearestEdgeBucket(flat, 10); b != -1 {
		t.Fatalf("a flat record has an edge at %d", b)
	}
}
