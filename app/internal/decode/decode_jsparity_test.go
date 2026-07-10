package decode

import (
	"encoding/json"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"open-sds/app/internal/testenv"
)

// TestJSDecoderParity guarantees the browser JS twins (internal/web/decode_*.js)
// decode byte-for-byte identically to the Go decoders, so the web overlay and the
// on-device LCD never disagree. It generates a battery of waveforms with the SAME
// generators the Go break/round-trip suites use, records each Go decode as ground
// truth, then replays the vectors through the JS twins under node and asserts an
// exact {ok, bytes} match. Skips when node is unavailable — a hard failure
// under CI_REQUIRE_BROWSER=1 (internal/testenv).
func TestJSDecoderParity(t *testing.T) {
	testenv.NeedNode(t)
	node, _ := exec.LookPath("node")

	type jsCfg map[string]interface{}
	type vec struct {
		Proto  string  `json:"proto"`
		Codes  []int   `json:"codes"`
		Codes2 []int   `json:"codes2,omitempty"` // second channel: I2C/SPI + autodetect
		ColT   float64 `json:"colT"`
		Cfg    jsCfg   `json:"cfg"`
		OK     bool    `json:"ok"`
		Bytes  []int   `json:"bytes"`
		Text   string  `json:"text"`
		Det    string  `json:"det,omitempty"` // autodetect: the protocol Go chose
	}
	u8 := func(c []uint8) []int {
		out := make([]int, len(c))
		for i, v := range c {
			out[i] = int(v)
		}
		return out
	}
	var vecs []vec
	add := func(proto string, codes []uint8, colT float64, cfg jsCfg, r Result) {
		b := r.Bytes
		if b == nil {
			b = []int{}
		}
		vecs = append(vecs, vec{Proto: proto, Codes: u8(codes), ColT: colT, Cfg: cfg,
			OK: r.OK, Bytes: b, Text: r.Text})
	}
	add2 := func(proto string, c1, c2 []uint8, colT float64, cfg jsCfg, r Result) {
		b := r.Bytes
		if b == nil {
			b = []int{}
		}
		vecs = append(vecs, vec{Proto: proto, Codes: u8(c1), Codes2: u8(c2), ColT: colT,
			Cfg: cfg, OK: r.OK, Bytes: b, Text: r.Text, Det: r.Proto})
	}

	// ---- Manchester: auto-bitrate across spb (incl. spb=5), both conventions.
	for _, spb := range []int{5, 8, 13, 20, 40} {
		for _, ieee := range []bool{true, false} {
			want := []int{0xAA, 0xB3, 0x2C, 0x47, 0x99}
			w, ct := bkBuild(want, ieee, true, 8, spb, 100000, 0, 0)
			add("manchester", w, ct, jsCfg{"ieee": ieee, "msb": true, "bits": 8},
				DecodeManchester(w, ct, ManchesterCfg{IEEE: ieee, MSB: true}))
		}
	}
	// constant-bit (idle-symmetry phase) + its control + alternating preamble only.
	{
		spb := 40
		ct := 1.0 / (float64(spb) * 100000.0)
		for _, tc := range []struct {
			ieee bool
			want []int
		}{
			{true, []int{0x00, 0x00, 0x00}}, {false, []int{0xFF, 0xFF, 0xFF}},
			{true, []int{0xFF, 0xFF, 0xFF}}, {false, []int{0x00, 0x00, 0x00}},
		} {
			w := manchesterWave(mBits(tc.want, true, 8), tc.ieee, spb)
			add("manchester", w, ct, jsCfg{"ieee": tc.ieee, "msb": true, "bits": 8},
				DecodeManchester(w, ct, ManchesterCfg{IEEE: tc.ieee, MSB: true}))
		}
	}

	// ---- MIL-1553: auto inference including a 0xAAAA payload and spb=5.
	for _, spb := range []int{5, 8, 20} {
		words := []int{0x1234, 0xAAAA}
		cmd := []bool{true, false}
		par := []int{mil1553OddParity(0x1234), mil1553OddParity(0xAAAA)}
		w := mil1553Wave(words, cmd, par, spb)
		ct := 1.0 / (float64(spb) * 1e6)
		add("mil1553", w, ct, jsCfg{}, DecodeMIL1553(w, ct, MIL1553Cfg{}))
	}

	// ---- FlexRay: a valid-CRC frame (explicit + auto) and a corrupted-CRC frame.
	{
		spb := 20
		good := brFlexFixCRC([]int{0x12, 0x34, 0x56, 0x78, 0x9A, 0xBC, 0xDE})
		w := brFlexFrame(good, spb, 8, 8, 8)
		ctE := ctForExact(10_000_000, spb)
		add("flexray", w, ctE, jsCfg{"bitrate": 10_000_000}, DecodeFlexRay(w, ctE, FlexRayCfg{Bitrate: 10_000_000}))
		add("flexray", w, 1e-8, jsCfg{}, DecodeFlexRay(w, 1e-8, FlexRayCfg{}))
		bad := append([]int{}, good...)
		bad[3] ^= 0x40 // flip a header-CRC-field bit
		wb := brFlexFrame(bad, spb, 8, 8, 8)
		add("flexray", wb, ctE, jsCfg{"bitrate": 10_000_000}, DecodeFlexRay(wb, ctE, FlexRayCfg{Bitrate: 10_000_000}))
	}

	// ---- SENT: a CRC-valid frame + a corrupted-CRC frame (both cfg.nibbles=8).
	{
		nibs := []int{0x1, 0xA, 0x5, 0xF, 0x0, 0xC, 0x3, 0}
		nibs[7] = sentCRC4(nibs[1:7])
		w := sentWave([][]int{nibs}, 6, 0, 0)
		add("sent", w, 1e-6, jsCfg{"nibbles": 8}, DecodeSENT(w, 1e-6, SENTCfg{Nibbles: 8}))
		bad := append([]int{}, nibs...)
		bad[7] ^= 0xF
		wb := sentWave([][]int{bad}, 6, 0, 0)
		add("sent", wb, 1e-6, jsCfg{"nibbles": 8}, DecodeSENT(wb, 1e-6, SENTCfg{Nibbles: 8}))
	}

	// ---- CAN: a standard data frame (stuff-bit + CRC-15 path).
	{
		spb := 20
		_, wire := canStdFrame(0x123, 3, []int{0xDE, 0xAD, 0xBE})
		codes := canRender(wire, spb, true, 8*spb, 8*spb)
		ct := 1.0 / (float64(spb) * 500000.0)
		add("canfd", codes, ct, jsCfg{"nominalBaud": 500000, "dominantLow": true},
			DecodeCANFD(codes, ct, CANFDCfg{NominalBaud: 500000, DominantLow: true}))
	}

	// ---- ARINC 429: a complete word (auto bit-rate, pulse-count gate).
	{
		spb := 40
		ct := 2.5e-7
		bits := arincMakeWord(0o107, 1, 0x5A5A, 2)
		var w []uint8
		arincIdle(&w, 6*spb)
		arincAppendWord(&w, bits, spb)
		arincIdle(&w, 6*spb)
		add("arinc429", w, ct, jsCfg{}, DecodeARINC429(w, ct, ARINC429Cfg{}))
	}

	// ---- UART: same 8N1 wave decoded under each parity setting; Go and JS must
	// agree on the stop-bit + parity-length handling even when the config is off.
	for _, par := range []string{"none", "even", "odd"} {
		spb := 16
		ct := 1e-6
		w := uartWave([]int{0x48, 0x69, 0x21}, spb)
		baud := int(1.0 / (float64(spb) * ct))
		add("uart", w, ct, jsCfg{"baud": baud, "parity": par},
			DecodeUART(w, ct, UARTCfg{Baud: baud, Parity: par}))
	}

	// ---- UART auto-baud on RINGY edges (the task-2 hardening): the cluster-walk
	// inference must land on the same spb — hence the same bytes — in Go and JS.
	{
		rrng := rand.New(rand.NewSource(0x21A6))
		for it := 0; it < 8; it++ {
			spb := []float64{12.0, 14.5, 16.0, 20.25}[it%4]
			want := make([]int, 5)
			for i := range want {
				want[i] = rrng.Intn(256)
			}
			w := buInjectRing(buRasterize(buBuildSeq(want, 8, "none", 8, 24, 1), spb, it%3), rrng, 3)
			ct := 1e-6
			add("uart", w, ct, jsCfg{"parity": "none"}, DecodeUART(w, ct, UARTCfg{}))
		}
		// Genuinely ambiguous two-tone pulses: both sides must refuse identically.
		var amb []uint8
		lv := uint8(210)
		for p := 0; p < 60; p++ {
			n := 11
			if p%2 == 1 {
				n = 17
			}
			for j := 0; j < n; j++ {
				amb = append(amb, lv)
			}
			lv = 250 - lv
		}
		add("uart", amb, 1e-6, jsCfg{}, DecodeUART(amb, 1e-6, UARTCfg{}))
	}

	// ---- SPI: all four modes + both bit orders, the HW 374-vs-372 sampling-
	// cadence shape (task 1), and an asymmetric-duty clock. Two-channel vectors.
	{
		ct := 2e-7
		msg := []int{0x48, 0x65, 0x6C, 0x6C, 0x6F}
		for _, mode := range [][2]bool{{false, false}, {false, true}, {true, false}, {true, true}} {
			for _, msb := range []bool{true, false} {
				clk, dat, _ := spiSynth([][]int{msg}, 8, msb, mode[0] == mode[1], 32, 0, 32)
				bo := "lsb"
				if msb {
					bo = "msb"
				}
				add2("spi", clk, dat, ct, jsCfg{"cpol": mode[0], "cpha": mode[1], "bitOrder": bo},
					DecodeSPI(clk, dat, ct, SPICfg{CPOL: mode[0], CPHA: mode[1], MSB: msb, Format: "hex"}))
			}
		}
		// The 374-vs-372 HW shape: partial 124-col first half-cycle + ~375-col
		// jittered periods (built like TestBreakSpiSamplingCadence).
		var clk, dat []uint8
		seg := func(c, d uint8, n int) {
			for i := 0; i < n; i++ {
				clk = append(clk, c)
				dat = append(dat, d)
			}
		}
		seg(210, 40, 124)
		seg(40, 40, 187)
		bit := 0
		for _, b := range []int{0xA7, 0x3C} {
			for k := 7; k >= 0; k-- {
				dv := uint8(40)
				if (b>>uint(k))&1 == 1 {
					dv = 210
				}
				seg(40, dv, 187+bit%2)
				seg(210, dv, 188)
				bit++
			}
		}
		seg(40, 40, 400)
		add2("spi", clk, dat, ct, jsCfg{"cpol": false, "cpha": false, "bitOrder": "msb"},
			DecodeSPI(clk, dat, ct, SPICfg{MSB: true, Format: "hex"}))
		aclk, adat := spiWaveAsym([]int{0x5A, 0x3C}, 2, 12)
		add2("spi", aclk, adat, ct, jsCfg{"cpol": false, "cpha": false, "bitOrder": "msb"},
			DecodeSPI(aclk, adat, ct, SPICfg{MSB: true, Format: "hex"}))
	}

	// ---- I2C: a full transaction, plus the channel swap (SCL<->SDA).
	{
		ct := 2e-7
		scl, sda := i2cWave(0x24, 0, []int{0x55, 0xAA}, 20)
		add2("i2c", scl, sda, ct, jsCfg{}, DecodeI2C(scl, sda, ct, I2CCfg{}))
		add2("i2c", sda, scl, ct, jsCfg{}, DecodeI2C(sda, scl, ct, I2CCfg{}))
	}

	// ---- Autodetect: the FINAL CHOICE (protocol + decoded bytes/text) must
	// match byte-for-byte across all ten protocols, on either channel, plus the
	// no-signal and corrupted-CRC shapes.
	{
		addAuto := func(c1, c2 []uint8, ct float64) {
			add2("autodetect", c1, c2, ct, jsCfg{"fmt": "hex"}, Autodetect(c1, c2, ct, "hex"))
		}
		uaW2 := uartWave([]int{0x48, 0x69, 0x55, 0xAA}, 40)
		clkW, datW := spiWave([]int{0x48, 0x69, 0x55, 0xAA}, 20)
		sclW, sdaW := i2cWave(0x24, 0, []int{0x55, 0xAA}, 20)
		manW2, manCT2 := bkBuild([]int{0xAA, 0xB3, 0x2C, 0x47, 0x99}, true, true, 8, 20, 100000, 0, 0)
		milW2 := mil1553Wave([]int{0x1234, 0xAAAA}, []bool{true, false},
			[]int{mil1553OddParity(0x1234), mil1553OddParity(0xAAAA)}, 20)
		nibs2 := []int{0x1, 0xA, 0x5, 0xF, 0x0, 0xC, 0x3, 0}
		nibs2[7] = sentCRC4(nibs2[1:7])
		sntW2 := sentWave([][]int{nibs2}, 6, 0, 0)
		_, cw2 := canStdFrame(0x123, 3, []int{0xDE, 0xAD, 0xBE})
		canW2 := canRender(cw2, 20, true, 160, 160)
		var arW2 []uint8
		arincIdle(&arW2, 240)
		arincAppendWord(&arW2, arincMakeWord(0o107, 1, 0x5A5A, 2), 40)
		arincIdle(&arW2, 240)
		usbW2 := usbWave([]usbPkt{{pid: 0xD, data: []int{0x12, 0x00}}, {pid: 0x2}}, 20)
		flxW2 := brFlexFrame(brFlexFixCRC([]int{0x12, 0x34, 0x56, 0x78, 0x9A, 0xBC, 0xDE}), 20, 8, 8, 8)

		addAuto(uaW2, nil, 1e-6)
		addAuto(clkW, datW, 2e-7)
		addAuto(datW, clkW, 2e-7) // SPI roles swapped
		addAuto(sclW, sdaW, 2e-7)
		addAuto(sdaW, sclW, 2e-7) // I2C roles swapped
		addAuto(manW2, nil, manCT2)
		addAuto(nil, manW2, manCT2)
		addAuto(milW2, nil, 1.0/(20.0*1e6))
		addAuto(nil, milW2, 1.0/(20.0*1e6))
		addAuto(sntW2, nil, 1e-6)
		addAuto(nil, sntW2, 1e-6)
		addAuto(canW2, nil, 1.0/(20.0*500000.0))
		addAuto(nil, canW2, 1.0/(20.0*500000.0))
		addAuto(arW2, nil, 2.5e-7)
		addAuto(nil, arW2, 2.5e-7)
		addAuto(usbW2, nil, 1.0/(20.0*1.5e6))
		addAuto(nil, usbW2, 1.0/(20.0*1.5e6))
		addAuto(flxW2, nil, ctForExact(10_000_000, 20))
		addAuto(nil, flxW2, ctForExact(10_000_000, 20))
		// No signal / corrupted integrity: the (possibly "off") choice must agree.
		flat := make([]uint8, 1500)
		for i := range flat {
			flat[i] = 128
		}
		addAuto(flat, nil, 1e-6)
		badCan := append([]uint8{}, canW2...)
		for i := 400; i < 420 && i < len(badCan); i++ {
			badCan[i] = 255 - badCan[i]
		}
		addAuto(badCan, nil, 1.0/(20.0*500000.0))
		// A bare clock (the constant-bit Manchester trap) must stay "off" in both.
		var sq []uint8
		for i := 0; i < 60*40; i++ {
			if (i/20)%2 == 0 {
				sq = append(sq, 210)
			} else {
				sq = append(sq, 40)
			}
		}
		addAuto(sq, nil, 1e-6)
	}

	// ---- Corruption fuzz: flip/truncate random regions of each base waveform and
	// require the JS twin to reproduce Go's {ok, bytes, text} EXACTLY. This drives
	// the reject/flag paths (bad CRC, stuff violations, framing errors, dropped
	// words, record-end truncation) that clean vectors never reach.
	rng := rand.New(rand.NewSource(0xDEC0DE))
	corrupt := func(base []uint8) []uint8 {
		w := append([]uint8{}, base...)
		for r := 0; r < 1+rng.Intn(3); r++ {
			if len(w) == 0 {
				break
			}
			lvl := []uint8{0, 40, 128, 210, 255}[rng.Intn(5)]
			start := rng.Intn(len(w))
			for j := start; j < start+1+rng.Intn(len(w)/8+1) && j < len(w); j++ {
				w[j] = lvl
			}
		}
		if rng.Intn(3) == 0 && len(w) > 40 { // truncate the tail (record-end paths)
			w = w[:40+rng.Intn(len(w)-40)]
		}
		return w
	}
	type baseCase struct {
		proto string
		colT  float64
		cfg   jsCfg
		w     []uint8
		dec   func([]uint8) Result
	}
	manW, manCT := bkBuild([]int{0xAA, 0xB3, 0x2C, 0x47, 0x99}, true, true, 8, 20, 100000, 0, 0)
	_, cw := canStdFrame(0x123, 3, []int{0xDE, 0xAD, 0xBE})
	canW := canRender(cw, 20, true, 160, 160)
	milW := mil1553Wave([]int{0x1234, 0xAAAA}, []bool{true, false},
		[]int{mil1553OddParity(0x1234), mil1553OddParity(0xAAAA)}, 20)
	flxW := brFlexFrame(brFlexFixCRC([]int{0x12, 0x34, 0x56, 0x78, 0x9A, 0xBC, 0xDE}), 20, 8, 8, 8)
	snibsF := []int{0x1, 0xA, 0x5, 0xF, 0x0, 0xC, 0x3, 0}
	snibsF[7] = sentCRC4(snibsF[1:7])
	sntW := sentWave([][]int{snibsF}, 6, 0, 0)
	var arW []uint8
	arincIdle(&arW, 240)
	arincAppendWord(&arW, arincMakeWord(0o107, 1, 0x5A5A, 2), 40)
	arincIdle(&arW, 240)
	uaW := uartWave([]int{0x48, 0x69, 0x21}, 16)
	uaEvenW := buRasterize(buBuildSeq([]int{0x48, 0x69, 0x21}, 8, "even", 8, 8, 2), 16, 8)
	uaOddW := buRasterize(buBuildSeq([]int{0x3C, 0x5A, 0xFF}, 8, "odd", 8, 8, 2), 16, 8)
	milCT := 1.0 / (20.0 * 1e6)
	flxCT := ctForExact(10_000_000, 20)
	canCT := 1.0 / (20.0 * 500000.0)
	uaCT := 1e-6
	uaBaud := int(1.0 / (16.0 * uaCT))
	bases := []baseCase{
		{"manchester", manCT, jsCfg{"ieee": true, "msb": true, "bits": 8}, manW,
			func(w []uint8) Result { return DecodeManchester(w, manCT, ManchesterCfg{IEEE: true, MSB: true}) }},
		{"mil1553", milCT, jsCfg{}, milW,
			func(w []uint8) Result { return DecodeMIL1553(w, milCT, MIL1553Cfg{}) }},
		{"flexray", flxCT, jsCfg{"bitrate": 10_000_000}, flxW,
			func(w []uint8) Result { return DecodeFlexRay(w, flxCT, FlexRayCfg{Bitrate: 10_000_000}) }},
		{"sent", 1e-6, jsCfg{"nibbles": 8}, sntW,
			func(w []uint8) Result { return DecodeSENT(w, 1e-6, SENTCfg{Nibbles: 8}) }},
		{"canfd", canCT, jsCfg{"nominalBaud": 500000, "dominantLow": true}, canW,
			func(w []uint8) Result { return DecodeCANFD(w, canCT, CANFDCfg{NominalBaud: 500000, DominantLow: true}) }},
		{"arinc429", 2.5e-7, jsCfg{}, arW,
			func(w []uint8) Result { return DecodeARINC429(w, 2.5e-7, ARINC429Cfg{}) }},
		{"uart", uaCT, jsCfg{"baud": uaBaud, "parity": "none"}, uaW,
			func(w []uint8) Result { return DecodeUART(w, uaCT, UARTCfg{Baud: uaBaud}) }},
		{"uart", uaCT, jsCfg{"baud": uaBaud, "parity": "even"}, uaEvenW,
			func(w []uint8) Result { return DecodeUART(w, uaCT, UARTCfg{Baud: uaBaud, Parity: "even"}) }},
		{"uart", uaCT, jsCfg{"baud": uaBaud, "parity": "odd"}, uaOddW,
			func(w []uint8) Result { return DecodeUART(w, uaCT, UARTCfg{Baud: uaBaud, Parity: "odd"}) }},
	}
	for _, bc := range bases {
		for it := 0; it < 24; it++ {
			w := corrupt(bc.w)
			add(bc.proto, w, bc.colT, bc.cfg, bc.dec(w))
		}
	}

	dir := t.TempDir()
	jf := filepath.Join(dir, "vecs.json")
	blob, err := json.Marshal(vecs)
	if err != nil {
		t.Fatalf("marshal vectors: %v", err)
	}
	if err := os.WriteFile(jf, blob, 0o644); err != nil {
		t.Fatalf("write vectors: %v", err)
	}
	out, err := exec.Command(node, "../web/decparity_check.mjs", jf).CombinedOutput()
	t.Logf("parity (%d vectors):\n%s", len(vecs), out)
	if err != nil {
		t.Fatalf("Go<->JS decoder parity failed: %v", err)
	}
	if !strings.Contains(string(out), "ALL PARITY OK") {
		t.Fatalf("parity checker did not report ALL PARITY OK")
	}
}
