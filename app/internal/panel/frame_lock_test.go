// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"math/rand"
	"testing"
	"time"

	"open-sds/app/internal/engine"
)

// A bus search and a decode of the screen run on a copy of the frame: the
// frame source's callback (the fan-out's read lock on the scope) is held
// only for the copy. Run on the snapshot itself, a search on a busy line
// held the lock for over a minute on the scope, and the LCD and the web
// page's status waited behind the next snapshot all that time.
// TRLC-LINKS: REQ-SDS-020, REQ-SDS-214
func TestBusSearchHoldsTheFrameOnlyToCopyIt(t *testing.T) {
	c, _, _ := newC(t)
	// a busy line: a UART's characters over noise-like level changes
	rng := rand.New(rand.NewSource(1))
	f := uartFrame([]string{"Hi!\r\n", "OK\r\n"}, 460800, 100e-6, 100e-9, 250000)
	for i := 0; i < len(f.C2); i++ {
		if rng.Intn(7) == 0 {
			f.C2[i] = uint8(rng.Intn(256))
		}
	}
	var held time.Duration
	c.SetFrameSource(func(fn func(*engine.Frame)) {
		t0 := time.Now()
		fn(f)
		held += time.Since(t0)
	})
	check := func(name string, run func() bool) {
		held = 0
		t0 := time.Now()
		if !run() {
			t.Fatalf("%s found nothing on the frame", name)
		}
		if total := time.Since(t0); held > total/4 || held > 50*time.Millisecond {
			t.Errorf("%s: the frame was held %v of the %v the work took", name, held, total)
		}
	}
	check("search", func() bool { _, _, ok := c.detectOnScreen(); return ok })
	set(t, c, "bus.protocol", "UART")
	set(t, c, "bus.baud", "460800")
	check("decode", func() bool { _, _, ok := c.decodeOnScreen(); return ok })
}
