// ENGMODEL-OWNER-UNIT: FU-APP-SOUND
package sound

import (
	"math"
	"runtime"
	"testing"
)

// The modulation page's four-second demodulation, the tuner and the
// spectrum run several times a second on the instrument: they reuse their
// working buffers instead of allocating megabytes each time (8 MB a
// demodulation kept the collector running under the app's memory limit).
// TRLC-LINKS: REQ-SDS-269, REQ-SDS-267, REQ-SDS-270
func TestAnalysesReuseTheirBuffers(t *testing.T) {
	if raceBuild {
		t.Skip("the race detector's sync.Pool drops buffers on purpose")
	}
	x := make([]float64, int(4*fsTest))
	for i := range x {
		x[i] = math.Sin(2 * math.Pi * 440 * float64(i) / fsTest)
	}
	run := func() {
		Demodulate(x, fsTest, 440, 1000)
		DetectPitch(x[:4096], fsTest, DefaultPitchOpts)
		AudioSpectrum(x, fsTest, 32768)
	}
	run()
	var a, b runtime.MemStats
	runtime.ReadMemStats(&a)
	for i := 0; i < 5; i++ {
		run()
	}
	runtime.ReadMemStats(&b)
	if per := (b.TotalAlloc - a.TotalAlloc) / 5; per > 1<<20 {
		t.Fatalf("%d kB allocated a round, want under 1 MB", per>>10)
	}
}
