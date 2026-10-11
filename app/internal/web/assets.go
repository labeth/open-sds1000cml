// ENGMODEL-OWNER-UNIT: FU-APP-WEB
package web

import (
	"bytes"
	"embed"
	"net/http"
	"path"
	"strings"
	"time"
)

// assetFS holds the served client: the page, every classic-script .js module,
// and the CSS. A single directory embed means a new script only needs its
// <script> tag in ui.html — no per-file embed/handler/route boilerplate. The
// phone remote (remote.html, remote_*.js, remote.css) adds its web app
// manifest, home-screen icons and the keep-awake videos.
//
//go:embed ui.html remote.html *.js *.css remote.webmanifest remote-*.png remote-awake.mp4 remote-awake.webm
var assetFS embed.FS

// assetTypes are the served files' content types by extension.
var assetTypes = map[string]string{
	".js":          "text/javascript; charset=utf-8",
	".css":         "text/css; charset=utf-8",
	".html":        "text/html; charset=utf-8",
	".webmanifest": "application/manifest+json",
	".png":         "image/png",
	".mp4":         "video/mp4",
	".webm":        "video/webm",
}

// serveAsset writes an embedded static file with revalidation caching. The OTA
// agent swaps the whole binary (embedded assets included), so a browser-cached
// asset must round-trip before it can meet a newer server's wire format.
// Returns false (writing nothing) when the file is not embedded.
// TRLC-LINKS: REQ-SDS-161
func serveAsset(w http.ResponseWriter, name string) bool {
	body, err := assetFS.ReadFile(name)
	if err != nil {
		return false
	}
	ct := assetTypes[path.Ext(name)]
	if ct == "" {
		ct = "text/plain; charset=utf-8"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(body)
	return true
}

// serveMedia writes an embedded manifest, icon or video with byte-range
// support: iPhone Safari plays a video only from a server that answers Range
// requests (the remote's keep-awake fallback).
// TRLC-LINKS: REQ-SDS-161, REQ-SDS-220
func serveMedia(w http.ResponseWriter, r *http.Request, name string) bool {
	body, err := assetFS.ReadFile(name)
	if err != nil {
		return false
	}
	w.Header().Set("Content-Type", assetTypes[path.Ext(name)])
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(body))
	return true
}

// staticName reports whether p is a bare asset filename the root serves (no
// path traversal; pages are served by their own routes).
// TRLC-LINKS: REQ-SDS-161
func staticName(p string) bool {
	if strings.Contains(p, "/") {
		return false
	}
	ext := path.Ext(p)
	_, ok := assetTypes[ext]
	return ok && ext != ".html"
}
