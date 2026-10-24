// ENGMODEL-OWNER-UNIT: FU-APP-LCD
package lcd

import (
	"strconv"
	"testing"
)

// hexPair accepts exactly the two-character tokens ParseUint(t, 16, 8) does.
// TRLC-LINKS: REQ-SDS-214
func TestHexPairMatchesParseUint(t *testing.T) {
	for a := 0; a < 256; a++ {
		for b := 0; b < 256; b++ {
			tok := string([]byte{byte(a), byte(b)})
			v, err := strconv.ParseUint(tok, 16, 8)
			got, ok := hexPair(tok)
			if ok != (err == nil) || ok && got != byte(v) {
				t.Fatalf("hexPair(%q) = %d,%v; ParseUint %d,%v", tok, got, ok, v, err)
			}
		}
	}
}
