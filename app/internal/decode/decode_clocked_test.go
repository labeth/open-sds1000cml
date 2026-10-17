// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"reflect"
	"testing"
)

// TRLC-LINKS: REQ-SDS-018
func TestDecodePS2RoundTrip(t *testing.T) {
	sr := 1e6
	l := newTwoLine(sr)
	l.add(1, 1, 300e-6)
	ps2Frame(l, 0x1C, false, false, 80e-6, 200e-6) // 'A' make code
	ps2Frame(l, 0xF0, false, false, 80e-6, 100e-6)
	ps2Frame(l, 0x1C, false, false, 80e-6, 500e-6)
	ps2Frame(l, 0xED, true, false, 80e-6, 500e-6) // host: set LEDs
	ps2Frame(l, 0xFA, false, false, 80e-6, 500e-6)
	ps2Frame(l, 0x33, false, true, 80e-6, 500e-6)
	r := DecodePS2(l.a.codes(), l.b.codes(), 1/sr, ClockedCfg{})
	if !r.OK || r.Text != "1C F0 1C HED ACK FA !33" {
		t.Fatalf("ok=%v %q (%s)", r.OK, r.Text, r.Error)
	}
}

// TRLC-LINKS: REQ-SDS-018
func TestDecodeSWDRoundTrip(t *testing.T) {
	sr := 100e6
	s := &swdLine{t: newTwoLine(sr), per: 1e-6}
	s.host(0, 0)
	s.lineReset()
	s.transfer(false, true, 0, 1, 0x2BA01477, false, 2)  // DP IDCODE read
	s.transfer(false, false, 8, 1, 0x000000F0, false, 0) // DP SELECT write
	s.transfer(true, true, 0xC, 2, 0, false, 2)          // AP read, WAIT
	s.transfer(true, true, 0xC, 1, 0xDEADBEEF, true, 2)  // AP read, bad parity
	s.transfer(true, false, 4, 1, 0x80000001, false, 4)  // AP write
	r := DecodeSWD(s.t.a.codes(), s.t.b.codes(), 1/sr, ClockedCfg{})
	want := "LINERESET RDP0 OK 2BA01477 WDP8 OK 000000F0 RAPC WAIT RAPC OK !DEADBEEF WAP4 OK 80000001"
	if !r.OK || r.Text != want {
		t.Fatalf("ok=%v\n %q\nwant\n %q (%s)", r.OK, r.Text, want, r.Error)
	}
}

// TRLC-LINKS: REQ-SDS-018
func TestDecodeMDIORoundTrip(t *testing.T) {
	sr := 50e6
	l := newTwoLine(sr)
	l.add(1, 1, 2e-6)
	mdioFrame(l, 400e-9, 32, 1, 2, 0x01, 0x02, 0x796D, 0) // clause 22 read
	mdioFrame(l, 400e-9, 32, 1, 1, 0x1F, 0x00, 0x1140, 0) // clause 22 write
	mdioFrame(l, 400e-9, 32, 0, 0, 0x02, 0x01, 0x0007, 0) // clause 45 address
	mdioFrame(l, 400e-9, 32, 0, 3, 0x02, 0x01, 0xBEEF, 0) // clause 45 read
	mdioFrame(l, 400e-9, 32, 1, 2, 0x05, 0x02, 0xFFFF, 1) // nobody answered
	r := DecodeMDIO(l.a.codes(), l.b.codes(), 1/sr, ClockedCfg{})
	want := "RD 01.02 796D WR 1F.00 1140 ADDR 02.01 0007 RD45 02.01 BEEF RD 05.02 !FFFF"
	if !r.OK || r.Text != want {
		t.Fatalf("ok=%v\n %q\nwant\n %q (%s)", r.OK, r.Text, want, r.Error)
	}
}

// TRLC-LINKS: REQ-SDS-018
func TestDecodeWiegandRoundTrip(t *testing.T) {
	sr := 200e3
	l := newTwoLine(sr)
	l.add(1, 1, 10e-3)
	wiegandFrame(l, wiegandBits(26, 12, 4660), 2e-3)
	wiegandFrame(l, wiegandBits(34, 1000, 65535), 1e-3)
	bad := []byte(wiegandBits(26, 7, 1))
	bad[25] ^= 1
	wiegandFrame(l, string(bad), 2e-3)
	wiegandFrame(l, "1010110011", 2e-3)
	r := DecodeWiegand(l.a.codes(), l.b.codes(), 1/sr, ClockedCfg{})
	if !r.OK || r.Text != "26b F12 C4660 34b F1000 C65535 26b F7 !C1 10b AC 03" {
		t.Fatalf("ok=%v %q (%s)", r.OK, r.Text, r.Error)
	}
	if !reflect.DeepEqual(r.Bytes[:4], []int{12, 4660, 1000, 65535}) {
		t.Fatalf("units %v", r.Bytes)
	}
}
