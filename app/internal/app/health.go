// ENGMODEL-OWNER-UNIT: FU-APP-APP

package app

import (
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"github.com/labeth/open-sds1000cml/app/internal/engine"
)

// heartbeatMode is what the health token currently attests (read by the
// diagnostic status line): "off" (fabric not verified — never written),
// "diag" (fabric verified and the engine alive, no coherent frames yet: the
// bring-up heartbeat, workplan §4.3) or "frames" (frames advance).
var heartbeatMode atomic.Value

// healthLoop implements the app side of the health contract (workplan §4.3):
// the token is written every ~500 ms once the fabric is VERIFIED. While the
// engine publishes coherent frames the token attests frames (keyed on the
// beat counter so holdoff pacing and recovery bring-up stay healthy); before
// the first coherent frames — the bring-up state where the converters may be
// silent — it is an EXPLICIT diag heartbeat: logged as such, tagged in the
// token, keyed on the engine's beats (the owner goroutine servicing Exec).
// The loop starts only once the fabric is verified (main stops before it
// otherwise). A wedged engine stops every write, so the agent relaunches us
// on the still-live fd.
// TRLC-LINKS: REQ-SDS-025
func healthLoop(e *engine.Engine, path string) {
	heartbeatMode.Store("off")
	var lastBeats uint64
	var lastWrite time.Time
	mode := "off"
	for {
		time.Sleep(100 * time.Millisecond)
		s := e.Snapshot()
		if s.Wedged {
			continue
		}
		want := "diag"
		if s.Coherent >= 3 && s.Frames >= 3 {
			want = "frames"
		}
		if want != mode {
			mode = want
			heartbeatMode.Store(mode)
			if mode == "frames" {
				logf("health: %d coherent frames — health token attests FRAMES", s.Coherent)
				startMark("frames")
			} else {
				logf("health: DIAG HEARTBEAT — fabric verified and engine alive, no coherent frames yet; token tagged mode=diag")
			}
		}
		beats := e.Beats()
		if beats == lastBeats || time.Since(lastWrite) < 450*time.Millisecond {
			continue
		}
		tok := fmt.Sprintf("mode=%s frames=%d beats=%d coherent=%d published=%d ts=%d\n",
			mode, s.Frames, beats, s.Coherent, s.Published, time.Now().UnixNano())
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, []byte(tok), 0o644); err == nil { //nolint:gosec // G306: the OTA agent reads the token
			if os.Rename(tmp, path) == nil { //nolint:gosec // G703: the path is the app's own health token, never a client's
				lastBeats, lastWrite = beats, time.Now()
			}
		}
	}
}
