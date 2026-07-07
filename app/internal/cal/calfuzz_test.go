package cal

import (
	"encoding/binary"
	"math/rand"
	"testing"
)

// Cal parse fuzz: a corrupt calibration.dat (right size, wrong content) must
// be rejected cleanly, never panic the boot. Also feeds checksum-valid random
// payloads to exercise the descramble + record index math for any bytes.
func TestCalParseFuzz(t *testing.T) {
	rng := rand.New(rand.NewSource(0xCA1))
	for i := 0; i < 5000; i++ {
		raw := make([]byte, fileSize, fileSize+16) // headroom for the wrong-size slice below
		rng.Read(raw)
		if i%2 == 0 {
			// make the checksum valid so Parse runs the full descramble + decode
			var sum uint32
			for _, b := range raw[4:] {
				sum += uint32(b)
			}
			binary.LittleEndian.PutUint32(raw[0:4], -sum) // word0 = -sum (mod 2^32)
		}
		// occasionally a wrong size to exercise the length gate
		buf := raw
		if i%7 == 0 {
			buf = raw[:rng.Intn(fileSize+8)] // exercise the length gate (cap has headroom)
		}
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("iter %d (len %d) panicked: %v", i, len(buf), r)
				}
			}()
			tab, err := Parse(buf)
			if err == nil && tab == nil {
				t.Fatalf("iter %d: nil table with nil error", i)
			}
		}()
	}
}
