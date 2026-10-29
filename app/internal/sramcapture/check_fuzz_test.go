// ENGMODEL-OWNER-UNIT: FU-APP-SRAMCAPTURE
package sramcapture

import (
	"encoding/json"
	"testing"
)

// Any configuration a web page can send (/api/decoded/start's JSON) is
// checked without a panic, and the same one twice gives the same answer.
// TRLC-LINKS: REQ-SDS-032, REQ-SDS-040, REQ-SDS-082
func FuzzConfigCheck(f *testing.F) {
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"UART":{"Enabled":true},"PostWords":100,"Normal":true}`))
	f.Add([]byte(`{"SPI":{"Enabled":true,"Length":2,"GapTicks":10},"DecodedEvents":true,"PostWords":4096}`))
	f.Fuzz(func(t *testing.T, b []byte) {
		var cfg Config
		if json.Unmarshal(b, &cfg) != nil {
			return
		}
		a, c := cfg.Check(), cfg.Check()
		if (a == nil) != (c == nil) {
			t.Fatalf("checked twice: %v then %v", a, c)
		}
	})
}
