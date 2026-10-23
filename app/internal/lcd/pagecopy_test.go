// ENGMODEL-OWNER-UNIT: FU-APP-LCD
package lcd

import (
	"bytes"
	"math/rand"
	"testing"
)

// copyPage copies exactly: every length and alignment, nothing past the end.
// TRLC-LINKS: REQ-SDS-021
func TestCopyPageExact(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	src := make([]byte, pageBytes+8)
	rng.Read(src)
	for _, n := range []int{0, 1, 63, 64, 65, 200, 4096, pageBytes} {
		for _, off := range []int{0, 1, 4} {
			dst := make([]byte, n+off+8)
			copyPage(dst[off:off+n], src[off:off+n])
			if !bytes.Equal(dst[off:off+n], src[off:off+n]) {
				t.Fatalf("n %d off %d: differs", n, off)
			}
			for _, b := range append(dst[:off:off], dst[off+n:]...) {
				if b != 0 {
					t.Fatalf("n %d off %d: wrote outside", n, off)
				}
			}
		}
	}
}
