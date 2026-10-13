// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"encoding/json"
	"os"
	"testing"
)

// A plain square with only ~30 edges on screen is a clock, not constant-bit
// Manchester (not "AUTO MANCH 3kb/s" for a 3 kHz square at 500 us/div).
// TRLC-LINKS: REQ-SDS-018
func TestAutodetectFewEdgeSquareIsNotManchester(t *testing.T) {
	sq := make([]uint8, 2048)
	for i := range sq {
		sq[i] = 40
		if (i/68)%2 == 0 { // ~30 edges
			sq[i] = 210
		}
	}
	if r := Autodetect(sq, nil, 2.432e-6, "hex"); r.Proto == "manchester" {
		t.Fatalf("a square wave auto-detected as Manchester: %+v", r)
	}
}

// A 115200 Bd UART on the 1 ms/div live envelope (under two samples a bit)
// carries no decodable protocol, not SENT on one chance CRC-4 among 18 syncs
// (a real capture).
// TRLC-LINKS: REQ-SDS-018
func TestAutodetectUndersampledUARTIsNotSENT(t *testing.T) {
	b, err := os.ReadFile("testdata/uart115200_envelope_1ms.json")
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		SampleS float64 `json:"sample_s"`
		C1      []int   `json:"c1"`
	}
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	c1 := make([]uint8, len(f.C1))
	for i, v := range f.C1 {
		c1[i] = uint8(v)
	}
	if r := Autodetect(c1, nil, f.SampleS, "hex"); r.Proto == "sent" {
		t.Fatalf("undersampled UART auto-detected as SENT: %.80q", r.Text)
	}
}

// Auto must not name a protocol whose decode has more errors than evidence:
// with every other decoder rejected, a garbage Manchester or UART decode
// (negative score) must not win. Real bench captures, both channels carrying
// the line as on the fixtures.
// TRLC-LINKS: REQ-SDS-018
func TestAutodetectRealCapturesNoGarbageWinner(t *testing.T) {
	for _, tc := range []struct{ file, want string }{
		{"mil1553_envelope_50us", ""},   // too coarse for a 1553 sync on this frame: off, not Manchester
		{"sent_envelope_1ms", ""},       // 1.6 samples a tick: off, not "MANCH 51 kb/s"
		{"sent_envelope_200us", "sent"}, // bad, good, bad CRC on screen: still SENT, not UART
	} {
		b, err := os.ReadFile("testdata/" + tc.file + ".json")
		if err != nil {
			t.Fatal(err)
		}
		var f struct {
			SampleS float64 `json:"sample_s"`
			C1      []int   `json:"c1"`
		}
		if err := json.Unmarshal(b, &f); err != nil {
			t.Fatal(err)
		}
		c := make([]uint8, len(f.C1))
		for i, v := range f.C1 {
			c[i] = uint8(v)
		}
		r, _ := AutodetectFast(c, c, f.SampleS, "hex")
		switch {
		case tc.want == "" && r.Proto != "off" && r.Proto != "mil1553":
			t.Errorf("%s: auto-detected %s (score %.0f), want no garbage winner", tc.file, r.Proto, scoreResult(r))
		case tc.want != "" && r.Proto != tc.want:
			t.Errorf("%s: auto-detected %s, want %s", tc.file, r.Proto, tc.want)
		}
	}
}
