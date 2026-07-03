package web

import "testing"

// Auto-measurement logic now lives in internal/measure (with its own thorough
// suite); the web layer only re-exports it. This covers the web-local helper.

func TestResampleEnv(t *testing.T) {
	v := make([]uint8, 800)
	for i := range v {
		v[i] = uint8(i % 256)
	}
	out := resampleEnv(v, 800, 400)
	if len(out) != 400 || out[0] != 0 || out[399] != int16(v[399*800/400]) {
		t.Fatalf("resample: len=%d out[399]=%d", len(out), out[399])
	}
}
