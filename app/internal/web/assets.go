// ENGMODEL-OWNER-UNIT: FU-APP-WEB

package web

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"github.com/labeth/open-dso-core/design"
	"github.com/labeth/open-dso-core/webclient"
	"hash/fnv"
	"io/fs"
	"net/http"
	"path"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
)

// assetFS is what the server serves: the web client every scope shares
// (open-dso-core/webclient: the pages and every classic-script .js module,
// the phone remote's manifest, icons and keep-awake videos) and the shared
// stylesheets (open-dso-core/design), all at the top level. A new script
// only needs its <script> tag in ui.html.
var assetFS = layeredFS{webclient.FS, design.CSS}

// layeredFS reads a file from the first layer that has it and lists every
// layer's files, the first layer's winning a name both have.
type layeredFS []fs.FS

// ReadFile is name's body from the first layer that has it.
// TRLC-LINKS: REQ-SDS-161
func (l layeredFS) ReadFile(name string) ([]byte, error) {
	for _, f := range l {
		if b, err := fs.ReadFile(f, name); err == nil {
			return b, nil
		}
	}
	return nil, &fs.PathError{Op: "read", Path: name, Err: fs.ErrNotExist}
}

// ReadDir lists dir across the layers, sorted by name.
// TRLC-LINKS: REQ-SDS-161
func (l layeredFS) ReadDir(dir string) ([]fs.DirEntry, error) {
	seen := map[string]bool{}
	var out []fs.DirEntry
	for _, f := range l {
		es, err := fs.ReadDir(f, dir)
		if err != nil {
			continue
		}
		for _, e := range es {
			if !seen[e.Name()] {
				seen[e.Name()] = true
				out = append(out, e)
			}
		}
	}
	slices.SortFunc(out, func(a, b fs.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	return out, nil
}

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

// assetTags are the embedded files' ETags (a page's of the page as served),
// hashed once at start-up. The pages are revalidated on every load
// (no-cache: 304 while the same) and name each script and stylesheet with
// its tag (?v=), and a file asked for by its current tag is cached for good
// (immutable): opening the page moves only the page, and an OTA update swaps
// the binary, the tags and so the URLs, so a cached script can never meet a
// newer server's wire format. A file asked for without its tag (the audio
// worklet, the analysis worker) is revalidated as before.
var assetTags, assetPages = tagAssets()

// tagAssets hashes every embedded file and makes the pages as served.
// TRLC-LINKS: REQ-SDS-161
func tagAssets() (map[string]string, map[string][]byte) {
	tags, pages := map[string]string{}, map[string][]byte{}
	tag := func(body []byte) string {
		h := fnv.New64a()
		_, _ = h.Write(body) // a hash never fails a write
		return fmt.Sprintf(`"%016x"`, h.Sum64())
	}
	files, _ := assetFS.ReadDir(".")
	for _, f := range files {
		if body, err := assetFS.ReadFile(f.Name()); err == nil {
			tags[f.Name()] = tag(body)
		}
	}
	for _, page := range []string{"ui.html", "remote.html"} {
		body, err := assetFS.ReadFile(page)
		if err != nil {
			continue
		}
		pages[page] = versionRefs(body, tags)
		tags[page] = tag(pages[page])
	}
	return tags, pages
}

// assetRef finds a page's script and stylesheet references.
var assetRef = regexp.MustCompile(`(src|href)="([A-Za-z0-9_.-]+\.(?:js|css))"`)

// versionRefs names each embedded script and stylesheet a page refers to
// with its tag: src="app.js" becomes src="app.js?v=<tag>".
// TRLC-LINKS: REQ-SDS-161
func versionRefs(page []byte, tags map[string]string) []byte {
	return assetRef.ReplaceAllFunc(page, func(m []byte) []byte {
		sub := assetRef.FindSubmatch(m)
		t, ok := tags[string(sub[2])]
		if !ok {
			return m
		}
		return []byte(fmt.Sprintf(`%s="%s?v=%s"`, sub[1], sub[2], strings.Trim(t, `"`)))
	})
}

// gzipped holds each compressible file compressed, made at its first
// request (the page's scripts are 1.1 MB, a quarter of it compressed).
var gzipped sync.Map

// gzipBody is name's body compressed (body as served).
// TRLC-LINKS: REQ-SDS-161
func gzipBody(name string, body []byte) []byte {
	if v, ok := gzipped.Load(name); ok {
		if gz, ok := v.([]byte); ok {
			return gz
		}
	}
	var b bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&b, gzip.BestCompression)
	// A bytes.Buffer never fails a write: the compressor cannot either.
	_, _ = zw.Write(body)
	_ = zw.Close()
	gzipped.Store(name, b.Bytes())
	return b.Bytes()
}

// Precompress compresses every text file once, a few seconds after the
// server starts (when the scope has its first frames): compressed on its
// first request instead, the page's first load after a start took two
// seconds on the scope's ARM.
// TRLC-LINKS: REQ-SDS-161
func Precompress() {
	time.Sleep(5 * time.Second)
	files, _ := assetFS.ReadDir(".")
	for _, f := range files {
		name := f.Name()
		if !compressible(name) {
			continue
		}
		body, ok := assetPages[name]
		if !ok {
			body, _ = assetFS.ReadFile(name)
		}
		gzipBody(name, body)
	}
}

// compressible reports the files served gzip-encoded to a browser that
// takes it (text; the images and videos are compressed already).
// TRLC-LINKS: REQ-SDS-161
func compressible(name string) bool {
	switch path.Ext(name) {
	case ".js", ".css", ".html", ".webmanifest":
		return true
	}
	return false
}

// serveAsset writes an embedded file: a page and an untagged request
// revalidated (no-cache; its ETag answers If-None-Match with 304), a
// request carrying the file's current tag (?v=) cached for good. Text goes
// gzip-encoded to a browser that accepts it. Byte ranges are served:
// iPhone Safari plays a video only from a server that answers Range
// requests (the remote's keep-awake). Returns false (writing nothing) when
// the file is not embedded.
// TRLC-LINKS: REQ-SDS-161, REQ-SDS-220
func serveAsset(w http.ResponseWriter, r *http.Request, name string) bool {
	body, ok := assetPages[name]
	if !ok {
		// Read once: ReadFile copies the whole embedded file every request.
		if b, ok := assetBodies.Load(name); ok {
			body, _ = b.([]byte)
		} else {
			var err error
			if body, err = assetFS.ReadFile(name); err != nil {
				return false
			}
			assetBodies.Store(name, body)
		}
	}
	ct := assetTypes[path.Ext(name)]
	if ct == "" {
		ct = "text/plain; charset=utf-8"
	}
	tag := assetTags[name]
	w.Header().Set("Content-Type", ct)
	if v := r.URL.Query().Get("v"); v != "" && v == strings.Trim(tag, `"`) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		// No Date: the scope's clock is not set (1970), and a browser takes a
		// response's age from its Date, so every file was decades stale and
		// revalidated; without one it counts from when it got the file.
		w.Header()["Date"] = nil
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	if compressible(name) {
		w.Header().Add("Vary", "Accept-Encoding")
		if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			body = gzipBody(name, body)
			w.Header().Set("Content-Encoding", "gzip")
			tag = strings.TrimSuffix(tag, `"`) + `-gz"`
		}
	}
	w.Header().Set("ETag", tag)
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(body))
	return true
}

// assetBodies holds the embedded files other than the pages, as read.
var assetBodies sync.Map // name -> []byte

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
