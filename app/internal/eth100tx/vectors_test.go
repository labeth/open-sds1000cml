package eth100tx

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestEmitVectors dumps the golden-model stage boundaries to files the RTL
// stage sims consume. Run with:
//
//	ETH100TX_VECTOR_DIR=/path go test ./internal/eth100tx -run TestEmitVectors -v
//
// Without the env var it writes into ./vectors next to the package so the
// files are versioned with the model. Each file is one value per line (or
// $readmemb/$readmemh-friendly), documented in vectors/README below.
func TestEmitVectors(t *testing.T) {
	dir := os.Getenv("ETH100TX_VECTOR_DIR")
	if dir == "" {
		dir = "vectors"
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name  string
		frame []byte
		seed  [11]byte
	}{
		{"arp", testFrames()["arp-like"], DefaultSeed},
		{"icmp", testFrames()["ip-icmp"], [11]byte{1, 1, 1, 0, 1, 0, 0, 1, 0, 0, 1}},
	}

	for _, c := range cases {
		tx := EncodeFrame(c.frame, EncodeOpts{Seed: c.seed, haveSeed: true, LeadIdle: 32, TrailIdle: 8})
		rx := DecodeSamples(tx.Samples)
		if rx.Err != nil || !rx.FCSOK {
			t.Fatalf("%s: self-decode failed err=%v fcsok=%v", c.name, rx.Err, rx.FCSOK)
		}

		base := filepath.Join(dir, c.name)

		// (a) 600 MSa/s ternary sample codes — signed decimal, one per line.
		writeLines(t, base+".samples", len(tx.Samples), func(i int) string {
			return fmt.Sprintf("%d", tx.Samples[i])
		})
		// Also a compact ternary form (+/0/-) for eyeballing.
		writeLines(t, base+".ternary", len(tx.Samples), func(i int) string {
			switch {
			case tx.Samples[i] > 0:
				return "+1"
			case tx.Samples[i] < 0:
				return "-1"
			default:
				return "0"
			}
		})
		// (b) MLT-3 symbols @125 Mbaud, one signed level per line.
		writeLines(t, base+".symbols", len(tx.Symbols), func(i int) string {
			return fmt.Sprintf("%d", tx.Symbols[i])
		})
		// recovered 125 Mbit scrambled bits (NRZ), 1 bit/line ($readmemb-ready).
		writeLines(t, base+".scrambled_bits", len(tx.ScrambledBits), func(i int) string {
			return fmt.Sprintf("%d", tx.ScrambledBits[i])
		})
		// keystream aligned to scrambled/plain bits.
		writeLines(t, base+".keystream", len(tx.Keystream), func(i int) string {
			return fmt.Sprintf("%d", tx.Keystream[i])
		})
		// descrambled plaintext NRZ bits.
		writeLines(t, base+".plain_bits", len(tx.PlainBits), func(i int) string {
			return fmt.Sprintf("%d", tx.PlainBits[i])
		})
		// 5B code groups: "<5-bit-binary> <label>" per line, SSD/ESD marked.
		writeLines(t, base+".code_groups", len(tx.CodeGroups), func(i int) string {
			return fmt.Sprintf("%05b %s", tx.CodeGroups[i].Bits, tx.CodeGroups[i].Label)
		})
		// MII nibbles (incl preamble/SFD/FCS), one hex nibble per line.
		writeLines(t, base+".mii_nibbles", len(tx.MIINibbles), func(i int) string {
			return fmt.Sprintf("%X", tx.MIINibbles[i])
		})
		// Final MAC frame bytes + FCS + verdict.
		writeFrameFile(t, base+".frame", c.frame, tx.FCS)

		t.Logf("%-5s frame=%dB  samples=%d  symbols=%d  bits=%d  codegroups=%d  FCS=0x%08X  lock@bit=%d  FCSOK=%v",
			c.name, len(c.frame), len(tx.Samples), len(tx.Symbols), len(tx.PlainBits),
			len(tx.CodeGroups), tx.FCS, rx.LockOffset, rx.FCSOK)
	}

	writeReadme(t, dir)
}

func writeLines(t *testing.T, path string, n int, line func(int) string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	for i := 0; i < n; i++ {
		fmt.Fprintln(w, line(i))
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
}

func writeFrameFile(t *testing.T, path string, frame []byte, fcs uint32) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	fmt.Fprintf(w, "# MAC frame bytes (dst..payload), one hex octet per line\n")
	for _, b := range frame {
		fmt.Fprintf(w, "%02X\n", b)
	}
	fmt.Fprintf(w, "# FCS (IEEE 802.3, on-wire little-endian octets):\n")
	for _, b := range fcsBytes(fcs) {
		fmt.Fprintf(w, "FCS %02X\n", b)
	}
	fmt.Fprintf(w, "FCS_VALUE 0x%08X\n", fcs)
	w.Flush()
}

func writeReadme(t *testing.T, dir string) {
	t.Helper()
	const readme = `# eth100tx golden-model vectors

Emitted by TestEmitVectors. Each <case> is arp|icmp. These pin the RTL PHY
decoder stage-by-stage; the RTL must reproduce every file bit-for-bit on the
matching input.

RX pipeline order (each stage consumes the previous file):

  <case>.samples        600 MSa/s ternary amplitude codes, signed decimal,
                        one sample/line. AmpPos=+1000, 0, AmpNeg=-1000.
                        4.8 samples/symbol (floor pattern 4,5,5,5,5). This is
                        the DECODER INPUT. Slice at +/-500.
  <case>.ternary        same stream sliced to +1/0/-1 (human aid).
  <case>.symbols        CDR output: one MLT-3 ternary level (-1/0/+1) per
                        125 Mbaud symbol. len(samples) collapses to len here.
  <case>.scrambled_bits MLT-3 decode (level change=1, hold=0): 125 Mbit NRZ
                        scrambled bits, 1/line, MSB-first within code groups.
  <case>.keystream      LFSR x^11+x^9+1 keystream (k[n]=k[n-9]^k[n-11]),
                        aligned to scrambled/plain bits. RX recovers this by
                        idle-lock; provided for the descrambler stage check.
  <case>.plain_bits     descrambled NRZ bits = scrambled XOR keystream.
  <case>.code_groups    5-bit groups aligned on /J/K/: "<bbbbb> <label>" per
                        line. Labels: I,J,K,T,R = control; 0..F = data nibble.
  <case>.mii_nibbles    decoded data nibbles (hex), low-nibble-first per octet,
                        including preamble (0x55 x7) + SFD (0xD5) + frame + FCS.
  <case>.frame          final MAC frame octets + FCS octets + FCS_VALUE.

Stage boundary invariants the RTL must satisfy (all verified in Go tests):
  * RX .symbols  == TX .symbols  (CDR is exact on the clean stream)
  * RX .scrambled_bits == TX .scrambled_bits
  * RX .plain_bits[lock:] == TX .plain_bits[lock:]  (descrambler idle-lock)
  * .code_groups start with I* then J K ... T R then I*
  * FCS verifies via CRC-32/ISO-HDLC residue 0x2144DF1C over frame||FCS.
`
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte(readme), 0o644); err != nil {
		t.Fatal(err)
	}
}
