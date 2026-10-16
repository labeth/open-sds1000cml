// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"compress/gzip"
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
		{"mil1553_raw_1us", ""},         // part of a word: off, not "UART 2000000, 1 byte" or "Manch, 1 byte"
		{"sent_envelope_1ms", ""},       // 1.6 samples a tick: off, not "MANCH 51 kb/s"
		{"sent_envelope_200us", "sent"}, // bad, good, bad CRC on screen: still SENT, not UART
		// a 115200 Bd UART repeating one message, read as SENT with one frame
		// of two passing its CRC by chance: still UART
		{"uart115200_repeat_envelope_200us", "uart"},
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

// Edge ringing on a bus at the full rate is no I2C clock: ARINC 429's two
// lines at 10us/div read as I2C on a 20 ns clock (START 00 W ACK 00 ACK ...),
// faster than any I2C runs (Ultra Fast-mode: 5 MHz).
// TRLC-LINKS: REQ-SDS-018
func TestAutodetectRingingIsNotI2C(t *testing.T) {
	b, err := os.ReadFile("testdata/arinc429_raw_10us.json")
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		SampleS float64 `json:"sample_s"`
		C1, C2  []int
	}
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	c1, c2 := make([]uint8, len(f.C1)), make([]uint8, len(f.C2))
	for i := range f.C1 {
		c1[i], c2[i] = uint8(f.C1[i]), uint8(f.C2[i])
	}
	if r := DecodeI2C(c1, c2, f.SampleS, I2CCfg{Format: "hex"}); scoreResult(r) <= 0 {
		t.Fatalf("the capture no longer reads as I2C at all (score %.0f): the test proves nothing", scoreResult(r))
	}
	if r, _ := AutodetectFast(c1, c2, f.SampleS, "hex"); r.Proto == "i2c" {
		t.Fatalf("ringing auto-detected as I2C: %.60q", r.Text)
	}
}

// SPI's data moves on its clock's edges: an FPGA's a nanosecond ahead of its
// clock, a decimated record's a sample, and SDA falling and rising "while SCL
// is high" reads as I2C STARTs and STOPs, outscoring the SPI decode. An I2C
// START or STOP sits well inside SCL's high level (0.26 us at the least):
// SPI with a 200 kHz clock at 200us/div, decimated to 256 ns, is SPI.
// TRLC-LINKS: REQ-SDS-018
func TestAutodetectSPIDataOnClockEdgesIsNotI2C(t *testing.T) {
	b, err := os.ReadFile("testdata/spi_decimated_200us.json")
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		SampleS float64 `json:"sample_s"`
		C1, C2  []int
	}
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	c1, c2 := make([]uint8, len(f.C1)), make([]uint8, len(f.C2))
	for i := range f.C1 {
		c1[i], c2[i] = uint8(f.C1[i]), uint8(f.C2[i])
	}
	i2c := DecodeI2C(c1, c2, f.SampleS, I2CCfg{Format: "hex"})
	if scoreResult(i2c) <= 0 {
		t.Fatalf("the capture no longer reads as I2C at all (score %.0f): the test proves nothing", scoreResult(i2c))
	}
	if !i2cConditionsOnEdges(i2c, new(sliceChannel(c1, 0, false)), f.SampleS) {
		t.Error("its STARTs and STOPs are not on SCL's edges")
	}
	if r, _ := AutodetectFast(c1, c2, f.SampleS, "hex"); r.Proto != "spi" {
		t.Fatalf("SPI auto-detected as %q: %.60q", r.Proto, r.Text)
	}
	// a real I2C bus: its STARTs and STOPs in the middle of SCL high
	scl, sda := i2cWave(0x24, 0, []int{0x55, 0xAA}, 20)
	if r := DecodeI2C(scl, sda, 2e-7, I2CCfg{Format: "hex"}); !r.OK || i2cConditionsOnEdges(r, new(sliceChannel(scl, 0, false)), 2e-7) {
		t.Errorf("a clean I2C transaction: ok %v, conditions on SCL's edges %v", r.OK, i2cConditionsOnEdges(r, new(sliceChannel(scl, 0, false)), 2e-7))
	}
}

// A UART reading needs evidence: one byte is a stop bit that checks on half of
// random lines, and a fifth of its bytes misframed is another protocol read
// as UART at any rate (part of a CAN frame: 500000 Bd, 3 bytes, 1 misframed);
// clean bytes at a standard rate weigh most. No UART runs at 100 MBd: that is
// ringing on another bus's edges.
// TRLC-LINKS: REQ-SDS-018
func TestUARTScoreNeedsEvidence(t *testing.T) {
	uart := func(bytes, misframed int, baud int) Result {
		r := Result{OK: true, Proto: "uart", Baud: baud, SPB: 20}
		for i := range bytes {
			r.Bytes = append(r.Bytes, i)
			kind := "data"
			if i < misframed {
				kind = "frame-error"
			}
			r.Spans = append(r.Spans, Span{Kind: kind})
		}
		return r
	}
	for _, c := range []struct {
		name string
		r    Result
		pass bool
	}{
		{"one byte", uart(1, 0, 115200), false},
		{"3 bytes, 1 misframed, standard rate", uart(3, 1, 500000), false},
		{"3 clean bytes, standard rate", uart(3, 0, 115200), true},
		{"5 bytes, 1 misframed, standard rate", uart(5, 1, 115200), true},
		{"10 clean bytes at 100 MBd: ringing", uart(10, 0, 100058636), false},
		{"10 clean bytes at 12 MBd", uart(10, 0, 12000000), true},
	} {
		if got := scoreResult(c.r) > 0; got != c.pass {
			t.Errorf("%s: scored %.0f", c.name, scoreResult(c.r))
		}
	}
	if clean, odd := scoreResult(uart(8, 0, 115200)), scoreResult(uart(8, 0, 66674)); clean-odd != 100 {
		t.Errorf("8 clean bytes: standard rate %.0f, odd rate %.0f; want 100 apart", clean, odd)
	}
}

// A clock's idle level is its gap between transfers (a run over twice its
// half-period, also cut short by the record's end); a screen of nothing but
// clocking says nothing of it and reads low (SPI mode 0), not whichever level a
// few samples more fell on.
// TRLC-LINKS: REQ-SDS-018
func TestIdleLevelFromTheGap(t *testing.T) {
	clock := func(lead int, lead0 uint8, halves int, tail int, tail0 uint8) []uint8 {
		var c []uint8
		put := func(v uint8, n int) {
			for range n {
				c = append(c, v)
			}
		}
		put(lead0, lead)
		v := uint8(200)
		for range halves {
			put(v, 50)
			v = 250 - v // 200 <-> 50
		}
		put(tail0, tail)
		return c
	}
	for _, c := range []struct {
		name string
		sig  []uint8
		want int
	}{
		{"nothing but clocking, a few samples more high", clock(20, 50, 41, 20, 50), 0},
		{"idling high between transfers", clock(400, 200, 40, 0, 0), 1},
		{"idling low, the gap cut short by the record's end", clock(10, 200, 41, 110, 50), 0},
	} {
		if got := idleLevel(new(sliceChannel(c.sig, 0, false))); got != c.want {
			t.Errorf("%s: idle %d, want %d", c.name, got, c.want)
		}
	}
}

// A noisy SPI mode 3 capture (data moving on the falling clock edges, clock
// idle high) is named mode 3, as the data's launch edges say and the web's
// detection names it. The compiler (go1.26.0 to 1.26.2, amd64 and arm) merged
// a copy of the clock's figures into the stack slot of the channels' while
// the data line's were still to be read, so the vote counted the clock
// against itself and both phases were tried: mode 2 won the tie.
// TRLC-LINKS: REQ-SDS-018
func TestAutodetectSPIPhaseFromLaunchEdges(t *testing.T) {
	raw, err := os.ReadFile("testdata/spi_mode3_noisy.json")
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		C1, C2 []uint8
		ColT   float64
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	r := Autodetect(v.C1, v.C2, v.ColT, "hex")
	if r.Proto != "spi" || !r.Roles.CPOL || !r.Roles.CPHA || r.Text != "8C 28 23 B0 50 E3 31 22 E4 F4" {
		t.Fatalf("%s %s %+v %q, want SPI mode 3: 8C 28 23 B0 ...", r.Proto, r.Src, r.Roles, r.Text)
	}
}

// ARINC 429 words read at a rate no ARINC 429 bus runs are another bus's
// pulses: a MIL-1553 bus searched at 500us/div was filled in as "ARINC
// 35.9kb/s". The same words at 100 kbit/s or 12.5 kbit/s are ARINC 429.
// TRLC-LINKS: REQ-SDS-018
func TestAutodetectARINCOnlyAtItsRates(t *testing.T) {
	words := func(baud int) Result {
		r := Result{OK: true, Proto: "arinc429", Baud: baud}
		for i := 0; i < 4; i++ {
			r.Spans = append(r.Spans, Span{I0: i * 40, I1: i*40 + 8, Kind: "addr"}, Span{I0: i*40 + 8, I1: i*40 + 32, Kind: "data"})
		}
		return r
	}
	if s := scoreResult(words(35900)); s > 0 {
		t.Fatalf("ARINC 429 at 35.9 kbit/s scores %.0f", s)
	}
	for _, b := range []int{100000, 12500, 14500} {
		if s := scoreResult(words(b)); s <= 0 {
			t.Fatalf("ARINC 429 at %d bit/s scores %.0f", b, s)
		}
	}
}

// loadGzJSON reads a recorded two-channel capture (sample_s, c1, c2).
// TRLC-LINKS: REQ-SDS-018
func loadGzJSON(t *testing.T, path string) (c1, c2 []uint8, sampleS float64) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	z, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		SampleS float64 `json:"sample_s"`
		C1, C2  []int
	}
	if err := json.NewDecoder(z).Decode(&v); err != nil {
		t.Fatal(err)
	}
	c1, c2 = make([]uint8, len(v.C1)), make([]uint8, len(v.C2))
	for i := range v.C1 {
		c1[i], c2[i] = uint8(v.C1[i]), uint8(v.C2[i])
	}
	return c1, c2, v.SampleS
}

// A 62.5 MBd UART that sends 32 characters (5 us) a millisecond, recorded at
// the full rate: 330 us of it holds the burst on under 2 % of the samples,
// and its low level stays under the decoders' histogram noise floor (no
// active signal). AutodetectFast cuts the record to the burst and names the
// UART, its spans back at the record's own samples.
// TRLC-LINKS: REQ-SDS-018
func TestAutodetectFastFindsASparseBurst(t *testing.T) {
	c1, c2, sampleS := loadGzJSON(t, "testdata/uart_62mbd_sparse.json.gz")
	if r := Autodetect(c1, c2, sampleS, "hex"); r.OK {
		t.Fatalf("the whole record already reads as %s: the fixture no longer shows the case", r.Proto)
	}
	r, st := AutodetectFast(c1, c2, sampleS, "hex")
	if r.Proto != "uart" || r.Baud < 61e6 || r.Baud > 64e6 || len(r.Bytes) < 16 {
		t.Fatalf("%s %d Bd, %d bytes: want the 62.5 MBd UART", r.Proto, r.Baud, len(r.Bytes))
	}
	k := int(st/sampleS + 0.5)
	if i := r.Spans[0].I0 * k; i < 40000 || i > 43000 {
		t.Errorf("first span at sample %d, want the burst's start near 40000", i)
	}
	if keep := activeBlocks(make([]uint8, 100000), nil); keep != nil {
		t.Errorf("a flat record cut to %d blocks", len(keep))
	}
}

// The bench's 104 MHz SPI flash look-alike at the full rate (2 ns, 4.8
// samples a clock): its data moves 7 ns after the falling edge, one place in
// the clock's cycle, and is SPI mode 0 with the clock on C2. Its edges' phases
// in whole samples spread over more than the quarter cycle the lock is
// judged in, and the pair read as a clock beside a line it does not clock
// (no bus on most records).
// TRLC-LINKS: REQ-SDS-018
func TestAutodetectFastSPIDataLocksToItsClock(t *testing.T) {
	c1, c2, sampleS := loadGzJSON(t, "testdata/spi_flash104_10us.json.gz")
	clk := clockScore(c2)
	d := sliceChannel(c1, 0, false)
	if share, n := phaseLock(&clk, &d); n < 12 || share < 0.9 {
		t.Errorf("data locked to the clock: share %.2f of %d edges, want over 0.9", share, n)
	}
	r := Autodetect(c1, c2, sampleS, "hex")
	if r.Proto != "spi" || r.Roles.ChA != 1 || r.Roles.CPOL || r.Roles.CPHA || !r.Roles.MSB || len(r.Bytes) < 100 {
		t.Fatalf("%s %s %+v, %d bytes: want SPI mode 0, clock C2, MSB first", r.Proto, r.Src, r.Roles, len(r.Bytes))
	}
}
