// ENGMODEL-OWNER-UNIT: FU-APP-FPGALOAD
package fpgaload

import "testing"

// nopPort takes writes and counts them: shiftAll's own cost.
type nopPort struct{ n int }

// TRLC-LINKS: REQ-SDS-005
func (p *nopPort) WriteCfg(uint16) error { p.n++; return nil }

// TRLC-LINKS: REQ-SDS-005
func (p *nopPort) ReadCfg() (uint16, error) { return 0, nil }

// TRLC-LINKS: REQ-SDS-005
func BenchmarkShiftAllPort(b *testing.B) {
	rbf := make([]byte, 65536)
	for i := range rbf {
		rbf[i] = byte(i * 37)
	}
	p := &nopPort{}
	for b.Loop() {
		if err := shiftAll(p, rbf, true, &Options{}); err != nil {
			b.Fatal(err)
		}
	}
}

// The per-word table shifts the same words as the per-bit loop.
// TRLC-LINKS: REQ-SDS-005
func TestShiftAllWordsAreTheBits(t *testing.T) {
	type rec struct{ w []uint16 }
	for _, rev := range []bool{false, true} {
		var got rec
		port := &recPort{}
		rbf := []byte{0x00, 0xff, 0x6a, 0x56, 0x81}
		if err := shiftAll(port, rbf, rev, &Options{}); err != nil {
			t.Fatal(err)
		}
		got.w = port.w
		var want []uint16
		for _, by := range rbf {
			if rev {
				by = bitrev(by)
			}
			for bit := 7; bit >= 0; bit-- {
				w := wordIdle
				if by>>uint(bit)&1 == 1 {
					w |= BitDATA0
				}
				want = append(want, w, w|BitDCLK)
			}
		}
		if len(got.w) != len(want) {
			t.Fatalf("rev %v: %d words, want %d", rev, len(got.w), len(want))
		}
		for i := range want {
			if got.w[i] != want[i] {
				t.Fatalf("rev %v word %d: %#x, want %#x", rev, i, got.w[i], want[i])
			}
		}
	}
}

// recPort records the words written.
type recPort struct{ w []uint16 }

// TRLC-LINKS: REQ-SDS-005
func (p *recPort) WriteCfg(v uint16) error { p.w = append(p.w, v); return nil }

// TRLC-LINKS: REQ-SDS-005
func (p *recPort) ReadCfg() (uint16, error) { return 0, nil }

// TRLC-LINKS: REQ-SDS-005
func BenchmarkIoctlXfer(b *testing.B) {
	p := &ioctlPort{fd: -1}
	for b.Loop() {
		p.xfer(reqWrite, wordIdle)
	}
}

// TRLC-LINKS: REQ-SDS-092
func BenchmarkResolveBitOrder(b *testing.B) {
	rbf := make([]byte, rbfHeaderOff+rbfHeaderLen)
	for i := 0; i < rbfPreambleLen; i++ {
		rbf[i] = 0xff
	}
	copy(rbf[rbfHeaderOff:], hdrNative)
	for b.Loop() {
		if _, _, err := resolveBitOrder(rbf, BitOrderReverse, true); err != nil {
			b.Fatal(err)
		}
	}
}
