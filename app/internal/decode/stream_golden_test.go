// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math/rand"
	"os"
	"sort"
	"strings"
	"testing"
)

// streamDigest is a stream's frames hashed whole: every frame's and span's
// sample indices, text, kind and value.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func streamDigest(frames []StreamFrame) string {
	h := sha256.New()
	var buf [8]byte
	put := func(v uint64) {
		binary.LittleEndian.PutUint64(buf[:], v)
		h.Write(buf[:])
	}
	for _, f := range frames {
		put(f.I0)
		put(f.I1)
		put(uint64(len(f.Spans)))
		for _, s := range f.Spans {
			put(s.I0)
			put(s.I1)
			put(uint64(int64(s.Val)))
			h.Write([]byte(s.Kind))
			h.Write([]byte{0})
			h.Write([]byte(s.Text))
			h.Write([]byte{0})
		}
	}
	return hex.EncodeToString(h.Sum(nil))[:24]
}

// streamGoldenRun is a long random stream of the case (dense or bursty)
// pushed in random chunks around the engine's push, and its frames.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func streamGoldenRun(tc streamCPUCase, seed int64, secs float64, bursty bool) []StreamFrame {
	a, b, ss := streamCPULines(tc, seed, secs, bursty)
	d := NewStreamDecoder(func(x, y []uint8) Result { return tc.c.dec(x, y, ss) }, int(tc.quiet/ss), int((tc.longest+tc.quiet)/ss))
	d.SetLead(int(tc.lead / ss))
	d.SetSubframes(tc.c.name == "spdif")
	rng := rand.New(rand.NewSource(seed))
	pushN := max(64, int(min(0.2, max(2*tc.quiet, 0.002))/ss))
	var out []StreamFrame
	for at := 0; at < len(a); {
		e := min(len(a), at+1+rng.Intn(2*pushN))
		var bl []uint8
		if tc.c.lines == 2 {
			bl = b[at:e]
		}
		out = append(out, d.Push(a[at:e], bl)...)
		at = e
	}
	return append(out, d.Flush()...)
}

// streamGoldenSecs is how long a stream each protocol's golden decodes:
// some thousands of frames, fewer for the buses at the finest buckets.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func streamGoldenSecs(tc streamCPUCase) float64 {
	switch {
	case tc.bucket < 0.2e-6:
		return 0.15
	case tc.bucket < 1e-6:
		return 0.6
	case tc.c.name == "dcf77":
		return 300
	}
	return max(3, min(40, 200*tc.longest))
}

// streamGolden is the stream decode's digest per protocol and traffic,
// made by the decoders before their loops ran on the edges and runs: the
// faster slicing and decoders must hand out the very same frames.
var streamGolden = map[string]string{
	"caliper/dense": "158:fde9ca9985c4c40b1e9fb1fc",
	"caliper/bursty": "28:20a86fc770e2e717e2bc9a8e",
	"can/dense": "2405:4788744272158a42b326621e",
	"can/bursty": "369:cae0e61f0a7006b18886b7d4",
	"cec/dense": "410:074a4e9e460662dd9f110e0e",
	"cec/bursty": "70:b3e5bc6fae3b646860ad0dfe",
	"cjtag/dense": "2887:211539aa0fcd7507fcf95897",
	"cjtag/bursty": "427:17f1938d22aec869917af490",
	"dali/dense": "171:1bd03e87e6a2cad77f06fe50",
	"dali/bursty": "28:9a8f74607678e37c2520035f",
	"dcf77/dense": "4:0cef6bfcf4450ffe658bcc27",
	"dcf77/bursty": "4:0cef6bfcf4450ffe658bcc27",
	"dht/dense": "240:6956fc54d7c9ecee71b93445",
	"dht/bursty": "44:9383721ab9cef4ecc7487e86",
	"dmx512/dense": "5545:5c424832a33652b6fc6477b3",
	"dmx512/bursty": "912:b80ce9d97303a59eac368c71",
	"dsi/dense": "126:af8428a6a103ab4dacb23bb7",
	"dsi/bursty": "24:a8bb3c44d4715de2d1030971",
	"em4100/dense": "206:bfa524c2d8ba1c9253a46237",
	"em4100/bursty": "36:df09e1f7b726fd008e37f627",
	"em4305/dense": "188:5bacc55675f91c78430fef45",
	"em4305/bursty": "32:69604883dbd8f448bd27c61e",
	"em4305-carrier/dense": "164:910ee8543db3dd7a41cb6697",
	"em4305-carrier/bursty": "28:ac4da93a8274148f9f27e1c9",
	"i2c/dense": "1030:416695f50b603b0f9a5e2dba",
	"i2c/bursty": "176:ecca1d7428535f505051e7ff",
	"irmp/dense": "206:ea50fd53cb400822dc725fab",
	"irmp/bursty": "35:b0649dd3b4620fb53767b59d",
	"irnec/dense": "102:945ca8cd86c27c6c5e75598c",
	"irnec/bursty": "19:68df7cbef16f383b62331e98",
	"irnec-carrier/dense": "101:4e0bd1800f756b18fe020d5c",
	"irnec-carrier/bursty": "20:f82c8220f5a5ae6a13c79fd4",
	"irrc5/dense": "97:da08f6304f6278c4f499f830",
	"irrc5/bursty": "18:ad076e45c7e7171ede65157f",
	"irrc6/dense": "98:24335009a70a719c8a1cf1da",
	"irrc6/bursty": "19:aed848515694a9b12823dc36",
	"irsirc/dense": "213:7e9befde571b20d86e9e3497",
	"irsirc/bursty": "36:67f61425239d05967bb54df4",
	"j1850/dense": "452:f797b6040478e26c467317dd",
	"j1850/bursty": "77:e96cff2757443acb657ad7c1",
	"lin/dense": "460:0c9d2a2f9635f03cc321922d",
	"lin/bursty": "80:0bee2419358d2a4c0f357e01",
	"manchester/dense": "5091:f97668da6940626edf2bd1d0",
	"manchester/bursty": "844:58cc43f8a7ecdf2822519a97",
	"maple/dense": "1801:6ab6e0444a027c91b0f2af46",
	"maple/bursty": "304:66a51ae90384c9fcea793d27",
	"mdio/dense": "1564:2a0380bb2776308a3b16335b",
	"mdio/bursty": "260:733ed22d87bc830547713d70",
	"midi/dense": "862:d28c4dbcb6a49a5ca055fa9a",
	"midi/bursty": "140:778fd21fdc949fcb9ff81aee",
	"miller/dense": "2490:999d63f4dc8ee9691ed8983f",
	"miller/bursty": "412:3ace1d66fd47324806689d74",
	"modbus/dense": "2558:09d2fc50a7f8941c813a3c25",
	"modbus/bursty": "420:14bc953fe91f376167a528ef",
	"morse/dense": "431:949de458d8185ce31d4cd0e3",
	"morse/bursty": "76:62688051a7f0d1acf95e99c9",
	"onewire/dense": "1165:8b7fd996d43c8048705364a7",
	"onewire/bursty": "196:a52183b55252f685a5eda482",
	"ook/dense": "277:3793c8e4d3071b6153670597",
	"ook/bursty": "48:8e4a58864edb645ac8fca7d7",
	"pdi/dense": "1910:4944357efac3192dfe55d1b0",
	"pdi/bursty": "320:d7f4a53b05a919e11d63c0e8",
	"pjdl/dense": "579:05ec5073ec977afe3e02d165",
	"pjdl/bursty": "96:a54f392b0d56ce1f31335085",
	"ps2/dense": "1899:5c419c2a569b8b76becba28d",
	"ps2/bursty": "324:84fe4e986982b8419598c0fa",
	"pwm/dense": "5696:f7cefee4b29c13136b3a6334",
	"pwm/bursty": "805:4ea632fc79fc4907a001218b",
	"qi/dense": "1056:87280ad9a4d3d5ea6e108c41",
	"qi/bursty": "176:2bfca3d0f9ffc54ab79796da",
	"quad/dense": "2605:c37838da7b37d167db0a687c",
	"quad/bursty": "432:ea54090dca3a673f5ba9bf16",
	"rcencode/dense": "218:48751ce44cae6a9f63587306",
	"rcencode/bursty": "38:af38378fb74237ac806ea74f",
	"sdcard/dense": "2392:03a70adafed3d8b4ea108ea1",
	"sdcard/bursty": "400:bd1d84c1d65e168c858b2b79",
	"sdq/dense": "1712:8a4c29f191d389753e8fb0d5",
	"sdq/bursty": "288:ec10b74b4e56fc0f33224d09",
	"spdif/dense": "14165:4705b0424bb1f5f42694ef07",
	"spdif/bursty": "2357:2b19ff09e27361464eda0f64",
	"spi/dense": "2903:c0614ae4d4c7ec8004f15b37",
	"spi/bursty": "496:4419ce4713321e4aa89e6c85",
	"stepper/dense": "1496:15fc1b44fac3aa9661a81256",
	"stepper/bursty": "224:0e249bef24e976a34821a1a5",
	"swd/dense": "2929:f325865b29e173f2d03e3e54",
	"swd/bursty": "609:d18de269019709ea3190f05c",
	"swim/dense": "396:ef2f5d75076947d52580b64d",
	"swim/bursty": "65:6194d68d19d27df5145756bf",
	"t55xx/dense": "275:ea903d782bf35d8a9914c624",
	"t55xx/bursty": "48:afaed9d87346f9ac3ae849e7",
	"uart/dense": "5321:1e21769edd069fec7a3c9879",
	"uart/bursty": "752:9fc7f13a0d5c2ade48a77c15",
	"usbpd/dense": "769:578bdd730391b4365a8bd7e5",
	"usbpd/bursty": "132:5e324ea5bc0ca0b2388b7849",
	"wiegand/dense": "256:0d7854bba78c93673b27aae9",
	"wiegand/bursty": "40:a6727608a8fad9024cdfc80f",
	"ws281x/dense": "851:65b74c9025fecac8fdb64984",
	"ws281x/bursty": "115:d1f920ca74eb96a3edf96805",
}

// TestStreamDecodeGolden decodes a long random stream of every protocol,
// dense and bursty, in random pushes, and checks its frames against the
// digests (SDS_STREAM_GOLDEN_PRINT=1 prints the table).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func TestStreamDecodeGolden(t *testing.T) {
	if testing.Short() {
		t.Skip("long streams")
	}
	names := map[string]bool{}
	for _, list := range [][]streamCase{streamCases, moreStreamCases, captureCases, streamCPUExtra} {
		for _, c := range list {
			names[c.name] = true
		}
	}
	var keys []string
	for n := range names {
		keys = append(keys, n)
	}
	sort.Strings(keys)
	var table strings.Builder
	for _, name := range keys {
		tc := streamCPUCaseOf(t, name)
		if tc.feature == 0 {
			continue
		}
		for _, bursty := range []bool{false, true} {
			key := fmt.Sprintf("%s/%v", name, map[bool]string{false: "dense", true: "bursty"}[bursty])
			frames := streamGoldenRun(tc, int64(len(name))*7+3, streamGoldenSecs(tc), bursty)
			got := fmt.Sprintf("%d:%s", len(frames), streamDigest(frames))
			fmt.Fprintf(&table, "\t%q: %q,\n", key, got)
			if want, ok := streamGolden[key]; ok && want != got {
				t.Errorf("%s: frames %s, want %s", key, got, want)
			} else if !ok && os.Getenv("SDS_STREAM_GOLDEN_PRINT") == "" {
				t.Errorf("%s: no golden digest", key)
			}
		}
	}
	if os.Getenv("SDS_STREAM_GOLDEN_PRINT") != "" {
		t.Log("\n" + table.String())
	}
}
