// ENGMODEL-OWNER-UNIT: FU-OTA-OTACTL
package otactl

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A relay switch through a local stand-in for the plug.
// TRLC-LINKS: REQ-SDS-112
func BenchmarkShellyOn(b *testing.B) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ison":true}`))
	}))
	defer srv.Close()
	s := NewShelly(strings.TrimPrefix(srv.URL, "http://"))
	for b.Loop() {
		if _, err := s.On(); err != nil {
			b.Fatal(err)
		}
	}
}
