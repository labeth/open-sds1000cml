package web

import (
	"embed"
	"net/http"
	"path"
	"strings"
)

// assetFS holds the served client: the page, every classic-script .js module,
// and the CSS. A single directory embed means a new script only needs its
// <script> tag in ui.html — no per-file embed/handler/route boilerplate.
//
//go:embed ui.html *.js *.css
var assetFS embed.FS

// serveAsset writes an embedded static file with revalidation caching. The OTA
// agent swaps the whole binary (embedded assets included), so a browser-cached
// asset must round-trip before it can meet a newer server's wire format.
// Returns false (writing nothing) when the file is not embedded.
func serveAsset(w http.ResponseWriter, name string) bool {
	body, err := assetFS.ReadFile(name)
	if err != nil {
		return false
	}
	ct := "text/plain; charset=utf-8"
	switch path.Ext(name) {
	case ".js":
		ct = "text/javascript; charset=utf-8"
	case ".css":
		ct = "text/css; charset=utf-8"
	case ".html":
		ct = "text/html; charset=utf-8"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(body)
	return true
}

// staticName reports whether p is a bare .js/.css filename (no path traversal).
func staticName(p string) bool {
	if strings.Contains(p, "/") {
		return false
	}
	return strings.HasSuffix(p, ".js") || strings.HasSuffix(p, ".css")
}
