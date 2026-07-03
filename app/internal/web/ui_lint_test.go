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
const inlineStyleBudget = 14

func TestInlineStyleBudget(t *testing.T) {
	n := strings.Count(readUIHTML(t), "style=\"")
	if n > inlineStyleBudget {
		t.Fatalf("inline style= count %d exceeds budget %d — the design system replaces inline styles with classes, it must not add them", n, inlineStyleBudget)
	}
	t.Logf("inline style= attributes: %d / budget %d (target 0 by Phase 2)", n, inlineStyleBudget)
}

// inlineScriptBudget: 0 — Phase 2b externalized the inline <script> to app.js so
// a strict CSP (script-src 'self', no 'unsafe-inline') holds. All scripts are now
// external same-origin (app.js/peaks.js/decode.js).
const inlineScriptBudget = 0

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

// TestContentSecurityPolicy pins the strict same-origin CSP added in Phase 2b:
// same-origin default, and SCRIPT restricted to 'self' with no 'unsafe-inline'
// (the XSS-relevant directive). style-src may keep 'unsafe-inline' until Phase 4
// removes the last inline display:none hooks.
func TestContentSecurityPolicy(t *testing.T) {
	fs := &fakeScope{stats: engine.Stats{Running: true}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	New(fs, nil, nil, nil).Handler().ServeHTTP(rec, req)
	csp := rec.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Fatal("no Content-Security-Policy header on the HTML document")
	}
	if !strings.Contains(csp, "default-src 'self'") {
		t.Errorf("CSP must be same-origin (default-src 'self'): %q", csp)
	}
	// The script-src directive must be present and must NOT allow inline script.
	script := ""
	for _, d := range strings.Split(csp, ";") {
		if d = strings.TrimSpace(d); strings.HasPrefix(d, "script-src") {
			script = d
		}
	}
	if script == "" {
		t.Errorf("CSP must set an explicit script-src: %q", csp)
	}
	if strings.Contains(script, "'unsafe-inline'") || strings.Contains(script, "'unsafe-eval'") {
		t.Errorf("script-src must not allow inline/eval: %q", script)
	}
}
