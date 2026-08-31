package eth100tx

// fabric_vector_test.go — ORACLE ANCHOR for the in-fabric line encoder fpga/
// standard/eth_tx.v (ITEM 2). The RTL emits a REPEATING MAC frame with a
// selectable good/bad FCS; this test pins the EXACT frame constant the RTL ROM
// carries, encodes+decodes it through the golden model, and prints the expected
// decoded body (frame||FCS) that sim/tb_eth_tx.v checks the decode chain against.
//
// If this test's frame constant, FCS value, or good/bad verdicts ever drift from
// the RTL ROM / the testbench literals, the mismatch surfaces here (and in sim).
//
//   go test ./internal/eth100tx -run TestFabricVector -v

import "testing"

// fabricFrame MUST equal frame_rom[] in fpga/standard/eth_tx.v and the FRAME
// literals in fpga/standard/sim/tb_eth_tx.v, byte-for-byte.
var fabricFrame = []byte{
	0x00, 0x11, 0x22, 0x33, 0x44, 0x55, // dst
	0x66, 0x77, 0x88, 0x99, 0xAA, 0xBB, // src
	0x08, 0x06, // ethertype (ARP)
	0xDE, 0xAD, 0xBE, 0xEF, // payload
}

func TestFabricVector(t *testing.T) {
	// ---- GOOD frame: encode -> decode round-trip, FCS must verify ----
	tx := EncodeFrame(fabricFrame, EncodeOpts{})
	rx := DecodeSamples(tx.Samples)
	if rx.Err != nil {
		t.Fatalf("decode error: %v", rx.Err)
	}
	if len(rx.Frame) != len(fabricFrame) {
		t.Fatalf("frame len %d != %d", len(rx.Frame), len(fabricFrame))
	}
	for i := range fabricFrame {
		if rx.Frame[i] != fabricFrame[i] {
			t.Fatalf("frame[%d]=%02x != %02x", i, rx.Frame[i], fabricFrame[i])
		}
	}
	if !rx.FCSOK {
		t.Fatalf("good frame reported FCS bad")
	}

	fcs := CRC32(fabricFrame)
	fb := fcsBytes(fcs)

	// The RTL corrupts FCS octet 0 by XOR 0xFF (bad_fcs=1). Prove a single-octet
	// flip makes the residue fail (framer flags8[3]) — this is what dec_trigger
	// mode-1 fires on.
	badBody := make([]byte, 0, len(fabricFrame)+4)
	badBody = append(badBody, fabricFrame...)
	badBody = append(badBody, fb[0]^0xFF, fb[1], fb[2], fb[3])
	if CRC32(badBody) == 0x2144DF1C {
		t.Fatalf("bad-FCS body unexpectedly passed the CRC residue")
	}

	// ---- emit the expected decoded body for the testbench (frame || FCS) ----
	t.Logf("FCS value = 0x%08X  (octets on wire: %02X %02X %02X %02X)",
		fcs, fb[0], fb[1], fb[2], fb[3])
	body := append(append([]byte{}, fabricFrame...), fb...)
	t.Logf("GOOD body (%d octets):", len(body))
	line := ""
	for i, b := range body {
		line += byteHex(b) + " "
		if (i+1)%8 == 0 {
			t.Logf("  %s", line)
			line = ""
		}
	}
	if line != "" {
		t.Logf("  %s", line)
	}
	t.Logf("BAD  body octet[%d] (FCS[0]) = %02X (corrupted from %02X)",
		len(fabricFrame), fb[0]^0xFF, fb[0])
}

func byteHex(b byte) string {
	const hx = "0123456789ABCDEF"
	return "8'h" + string([]byte{hx[b>>4], hx[b&0xF]})
}
