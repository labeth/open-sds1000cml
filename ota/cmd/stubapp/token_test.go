// ENGMODEL-OWNER-UNIT: FU-OTA-STUBAPP
package main

import (
	"fmt"
	"testing"
)

// TRLC-LINKS: REQ-SDS-177
func TestHealthTokenFormat(t *testing.T) {
	if got, want := string(healthToken(nil, 42, -7)), fmt.Sprintf("frames=%d ts=%d\n", 42, -7); got != want {
		t.Fatalf("%q, want %q", got, want)
	}
}
