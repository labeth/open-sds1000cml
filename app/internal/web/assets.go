// ENGMODEL-OWNER-UNIT: FU-APP-WEB
package web

import (
	"bytes"
	"embed"
	"fmt"
	"hash/fnv"
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

// assetTags are the embedded files' ETags, hashed once at start-up. A
// browser revalidates every file (no-cache) and gets 304 Not Modified while
// it holds the same one, so opening a page moves only what changed since the
// last visit; an OTA update swaps the binary and with it the tags.
var assetTags = tagAssets()

// tagAssets hashes every embedded file.
// TRLC-LINKS: REQ-SDS-161
func tagAssets() map[string]string {
	tags := map[string]string{}
	files, _ := assetFS.ReadDir(".")
	for _, f := range files {
		if body, err := assetFS.ReadFile(f.Name()); err == nil {
			h := fnv.New64a()
			h.Write(body)
			tags[f.Name()] = fmt.Sprintf(`"%016x"`, h.Sum64())
		}
	}
	return tags
}

// serveAsset writes an embedded file with revalidation caching: its ETag
// answers If-None-Match with 304. The OTA agent swaps the whole binary
// (embedded assets included), so a browser-cached asset must round-trip
// before it can meet a newer server's wire format. Byte ranges are served:
// iPhone Safari plays a video only from a server that answers Range
// requests (the remote's keep-awake). Returns false (writing nothing) when
// the file is not embedded.
// TRLC-LINKS: REQ-SDS-161, REQ-SDS-220
func serveAsset(w http.ResponseWriter, r *http.Request, name string) bool {
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
	w.Header().Set("ETag", assetTags[name])
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
