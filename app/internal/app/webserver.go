// ENGMODEL-OWNER-UNIT: FU-APP-APP

package app

import (
	"net/http"
	"time"
)

// webHeaderTimeout bounds the reading of a request's header: a peer that
// opens connections and never finishes a header would otherwise hold each
// one, and its goroutine, for good. A parked long-poll is past its header and
// is not affected.
const webHeaderTimeout = 10 * time.Second

// newWebServer is the web UI's server on listen: the header read is bounded,
// nothing else is (the long-poll parks requests; writes are bounded per
// response inside the handlers).
// TRLC-LINKS: REQ-SDS-161
func newWebServer(listen string, h http.Handler) *http.Server {
	return &http.Server{Addr: listen, Handler: h, ReadHeaderTimeout: webHeaderTimeout}
}
