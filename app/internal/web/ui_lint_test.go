package web

import (
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"open-sds/app/internal/engine"
)

// These are ALWAYS-RUN pure-Go guardrails for the UI/UX refactor. They must NOT
// depend on node/Playwright (the *_browser.mjs suite self-skips when the browser
// is absent, e.g. on the device or a bare CI), so the load-bearing invariants
// live here where `go test ./...` always executes them. See app/docs/ui-architecture.md.

func readUIHTML(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("ui.html")
	if err != nil {
		t.Fatalf("read ui.html: %v", err)
	}
	return string(b)
}

// inlineStyleBudget RATCHETS down as the design-system migration replaces inline
// style= attributes with token-driven classes. It can only ever be LOWERED — a
// refactor that adds inline styles fails immediately. Phase 2 migrated the static
// visual styles to primitives (66→22). The remainder is: ~14 `display:none` hooks
// (kept inline per the load-bearing contract until Phase 4 swaps them to the
// [hidden] attribute), 2 JS-template styles (→ classList in Phase 4), and a few
// singletons. Target 0 after Phase 4.
const inlineStyleBudget = 22

func TestInlineStyleBudget(t *testing.T) {
	n := strings.Count(readUIHTML(t), "style=\"")
	if n > inlineStyleBudget {
		t.Fatalf("inline style= count %d exceeds budget %d — the design system replaces inline styles with classes, it must not add them", n, inlineStyleBudget)
	}
	t.Logf("inline style= attributes: %d / budget %d (target 0 by Phase 2)", n, inlineStyleBudget)
}

// inlineScriptBudget: the page currently ships one inline <script> block. Phase 2
// externalizes it to an ES module so a strict CSP (no 'unsafe-inline') is
// possible. Ratchets to 0 then.
const inlineScriptBudget = 1

func TestInlineScriptBudget(t *testing.T) {
	// Count opening <script> tags with NO src attribute (inline blocks).
	html := readUIHTML(t)
	inline := 0
	for _, seg := range strings.Split(html, "<script")[1:] {
		head := seg
		if i := strings.IndexByte(seg, '>'); i >= 0 {
			head = seg[:i]
		}
		if !strings.Contains(head, "src=") {
			inline++
		}
	}
	if inline > inlineScriptBudget {
		t.Fatalf("inline <script> blocks %d exceeds budget %d", inline, inlineScriptBudget)
	}
	t.Logf("inline <script> blocks: %d / budget %d (target 0 by Phase 2 for strict CSP)", inline, inlineScriptBudget)
}

// TestContentSecurityPolicy pins the CSP invariant. Phase 2 adds a strict
// same-origin CSP once the inline <script>/style are externalized. Until then
// this test documents the target and SKIPS rather than failing the build; when
// Phase 2 lands, drop the skip so a missing/weakened CSP fails.
func TestContentSecurityPolicy(t *testing.T) {
	fs := &fakeScope{stats: engine.Stats{Running: true}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	New(fs, nil, nil, nil).Handler().ServeHTTP(rec, req)
	csp := rec.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Skip("CSP not added yet (Phase 2 externalizes the inline script/style, then adds a strict same-origin CSP)")
	}
	// Phase 2+ invariants: must be same-origin and forbid inline script.
	if !strings.Contains(csp, "default-src 'self'") {
		t.Errorf("CSP must be same-origin (default-src 'self'): %q", csp)
	}
	if strings.Contains(csp, "script-src") && strings.Contains(csp, "'unsafe-inline'") {
		t.Errorf("CSP must not allow 'unsafe-inline' script: %q", csp)
	}
}
