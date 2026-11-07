// ENGMODEL-OWNER-UNIT: FU-APP-WEB

package webscope

import "github.com/labeth/open-dso-core/webserver"

// Server is the SDS1000CML+ scope mounted on the shared web server as the
// app mounts it (with the runtime profiles): what the tests serve.
type Server struct {
	*Scope
	*webserver.Server
}

// newServer mounts the scope over the engine sc, front end fe, panel p and
// screen as the app does.
// TRLC-LINKS: REQ-SDS-161
func newServer(sc Engine, fe Analog, p Panel, screen func() []byte) *Server {
	scope := New(sc, fe, p, screen)
	return &Server{Scope: scope, Server: webserver.New(scope, webserver.WithPprof())}
}

// screenCols is the server's frame columns when a page names none.
const screenCols = 800
