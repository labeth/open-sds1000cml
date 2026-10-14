// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import "testing"

// A bit rate read from the edges within 1.5 % of the bus's standard rate is
// that rate: the bench MIL-1553 bus read "999kb/s", FlexRay "9.88Mb/s" and
// "10.1Mb/s", USB LS "1.51Mb/s" in BUS's notice. Far from every standard rate it stands.
// TRLC-LINKS: REQ-SDS-018
func TestBusRatesSnapToStandard(t *testing.T) {
	for _, c := range []struct {
		baud  int
		rates []float64
		want  int
	}{{999000, []float64{1e6}, 1000000}, {9880000, []float64{2.5e6, 5e6, 10e6}, 10000000},
		{10100000, []float64{2.5e6, 5e6, 10e6}, 10000000}, {101200, []float64{12.5e3, 100e3}, 100000},
		{970000, []float64{1e6}, 970000}, {1510000, []float64{1.5e6, 12e6}, 1500000}} {
		if got := snapRate(c.baud, c.rates...); got != c.want {
			t.Errorf("snapRate(%d) = %d, want %d", c.baud, got, c.want)
		}
	}
	words := []int{0xA55A, 0x5AA5}
	par := []int{mil1553OddParity(words[0]), mil1553OddParity(words[1])}
	const spb = 40
	w := mil1553Wave(words, []bool{true, false}, par, spb)
	r := DecodeMIL1553(w, 1/(spb*1.008e6), MIL1553Cfg{}) // edges read 0.8 % fast
	if !r.OK || r.Baud != 1000000 {
		t.Fatalf("MIL-1553 read %d bit/s (ok %v %s), want 1000000", r.Baud, r.OK, r.Error)
	}
}
