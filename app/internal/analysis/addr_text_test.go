// ENGMODEL-OWNER-UNIT: FU-APP-ANALYSIS
package analysis

import "testing"

// The BUS lists write each bus's address in its own terms: the bench ARINC
// labels 205 and 310 (octal, as the strip writes them) read "85 W" and
// "C8 W", a CAN data frame "123 W" (no direction on CAN).
// TRLC-LINKS: REQ-SDS-214
func TestAddrText(t *testing.T) {
	for _, c := range []struct {
		proto string
		p     Packet
		want  string
	}{
		{"i2c", Packet{Addr: 0x24, HasAddr: true}, "24 W"},
		{"i2c", Packet{Addr: 0x24, HasAddr: true, Read: true}, "24 R"},
		{"canfd", Packet{Addr: 0x123, HasAddr: true}, "123"},
		{"canfd", Packet{Addr: 0x100, HasAddr: true, Read: true}, "100 R"},
		{"arinc429", Packet{Addr: 0o310, HasAddr: true}, "310"},
		{"arinc429", Packet{Addr: 0o205, HasAddr: true}, "205"},
		{"mil1553", Packet{Addr: 5, HasAddr: true, Read: true}, "RT5 T"},
		{"usbls", Packet{Addr: 0x69, HasAddr: true}, "69"},
		{"uart", Packet{}, ""},
	} {
		if got := AddrText(c.proto, c.p); got != c.want {
			t.Errorf("%s %+v: %q, want %q", c.proto, c.p, got, c.want)
		}
	}
}
