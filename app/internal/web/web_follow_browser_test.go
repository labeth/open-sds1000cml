// ENGMODEL-OWNER-UNIT: FU-APP-WEB
package web

import (
	"os"
	"sync"
	"testing"

	"open-sds/app/internal/testenv"
)

// rigInverter is the display invert the CH tabs' Invert rows drive (SCPI's
// INVS shadow on the device).
type rigInverter struct {
	mu  sync.Mutex
	inv [2]bool
}

// TRLC-LINKS: REQ-SDS-211
func (i *rigInverter) Inverted() [2]bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.inv
}

// TRLC-LINKS: REQ-SDS-211
func (i *rigInverter) SetInverted(ch int, on bool) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.inv[ch&1] = on
}

// TestWebFollowsTheScopeSheet: the desktop page shows settings changed on the
// scope or the phone without a reload, and sets them on the scope (Stack
// master must not write the page's stale N over the scope's master depth).
// TRLC-LINKS: REQ-SDS-211
func TestWebFollowsTheScopeSheet(t *testing.T) {
	testenv.NeedNode(t)
	for _, f := range []string{"web_follow_browser.mjs", "scope_po.mjs"} {
		if _, err := os.ReadFile(f); err != nil {
			t.Fatal(err)
		}
	}
	r := newRemoteRig(t)
	r.pc.SetInverter(&rigInverter{})
	wsDriver(t, "web_follow_browser.mjs", r.app.URL)
}
