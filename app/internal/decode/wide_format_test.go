package decode

import (
	"strings"
	"testing"
)

// TRLC-LINKS: REQ-SDS-018
func TestWideWordFormatting(t *testing.T) {
	for _, tc := range []struct {
		v, bits      int
		format, want string
	}{
		{0x1234, 16, "hex", "1234"}, {0x0ff0, 16, "hex", "0FF0"}, {0x148, 9, "hex", "148"},
		{0x148, 9, "ascii", "."}, {0x148, 9, "both", "148"}, {0x48, 16, "both", "0048'H"},
		{0x101, 9, "bin", "100000001"}, {0x101, 9, "dec", "257"},
	} {
		if got := FmtWord(tc.v, tc.bits, tc.format); got != tc.want {
			t.Fatalf("%+v: %q", tc, got)
		}
	}
	w := manchesterWave(mBits([]int{0x1234, 0x5678}, true, 16), true, 40)
	r := DecodeManchester(w, 1e-6, ManchesterCfg{Bitrate: 25000, IEEE: true, MSB: true, Bits: 16})
	if !r.OK || !strings.Contains(r.Text, "1234 5678") {
		t.Fatalf("wide transcript: %+v", r)
	}
}
