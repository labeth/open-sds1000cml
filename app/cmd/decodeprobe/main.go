// decodeprobe: pull the scope's raw capture and run a protocol decoder on it —
// HW verification that a real FPGA-generated signal decodes to the generator's
// known payload. Usage: decodeprobe [-addr host:port] <proto> [arg]
// (SCOPE_ADDR env overrides the default; -addr overrides both.)
package main

import (
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"open-sds/app/internal/decode"
)

// scopeAddr resolves the target: -addr flag > SCOPE_ADDR env > the default.
var addrFlag = flag.String("addr", "", "scope address host:port (default $SCOPE_ADDR or 192.168.1.209:8080)")

func scopeAddr() string {
	if *addrFlag != "" {
		return *addrFlag
	}
	if a := os.Getenv("SCOPE_ADDR"); a != "" {
		return a
	}
	return "192.168.1.209:8080"
}

func fetch() (n int, c1, c2 []uint8, sampleS float64) {
	resp, err := http.Get("http://" + scopeAddr() + "/api/frame.bin?raw=1&since=0")
	if err != nil {
		fmt.Println("fetch:", err)
		os.Exit(1)
	}
	d, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if len(d) < 8 {
		fmt.Println("short frame")
		os.Exit(1)
	}
	hlen := binary.LittleEndian.Uint32(d[4:8])
	var hdr struct {
		Cols    int     `json:"cols"`
		SampleS float64 `json:"sample_s"`
	}
	json.Unmarshal(d[8:8+hlen], &hdr)
	n = hdr.Cols
	pay := d[8+int(hlen):]
	if n <= 0 || len(pay) < 2*n {
		fmt.Printf("no payload (cols=%d payload=%d)\n", n, len(pay))
		os.Exit(1)
	}
	c1 = make([]uint8, n)
	c2 = make([]uint8, n)
	copy(c1, pay[:n])
	copy(c2, pay[n:2*n])
	return n, c1, c2, hdr.SampleS
}

// expected generator payloads (the bytes the decoder should surface somewhere)
var expect = map[string][]int{
	"manchester": {0x4D, 0x31, 0x55, 0xAA},
	"uart":       {0x48, 0x65, 0x6C, 0x6C, 0x6F, 0x20, 0x77, 0x6F, 0x72, 0x6C, 0x64, 0x21}, // "Hello world!"
	"spi":        {0x48, 0x69, 0x20, 0x55, 0xAA, 0x0F, 0xF0, 0x0A},
	"i2c":        {0x55, 0xAA, 0x0F, 0xF0},
	"can":        {0xAB, 0xCD},
	"mil1553":    {0x1234},
	"flexray":    {0x12, 0x34, 0x56, 0x78, 0x9A, 0xBC, 0xDE, 0xF0},
	"usbls":      {0x55},
	"sent":       {}, // nibble stream — just report
}

func contains(hay, needle []int) bool {
	if len(needle) == 0 {
		return true
	}
	for i := 0; i+len(needle) <= len(hay); i++ {
		ok := true
		for j := range needle {
			if hay[i+j] != needle[j] {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

func main() {
	flag.Parse()
	proto := "manchester"
	if flag.NArg() > 0 {
		proto = flag.Arg(0)
	}
	n, c1, c2, sampleS := fetch()
	lo, hi := 255, 0
	for _, v := range c1 {
		if int(v) < lo {
			lo = int(v)
		}
		if int(v) > hi {
			hi = int(v)
		}
	}
	// diagnostics
	if proto == "raw" {
		start, cnt := 0, 120
		if flag.NArg() > 1 {
			fmt.Sscanf(flag.Arg(1), "%d", &start)
		}
		if flag.NArg() > 2 {
			fmt.Sscanf(flag.Arg(2), "%d", &cnt)
		}
		for i := start; i < start+cnt && i < n; i++ {
			fmt.Printf("%d:%d ", i, c1[i])
		}
		fmt.Println()
		return
	}
	if proto == "dbg" {
		th := (lo + hi) / 2
		hist := map[int]int{}
		last := -1
		prev := c1[0] >= uint8(th)
		for i := 1; i < n; i++ {
			cur := c1[i] >= uint8(th)
			if cur != prev {
				if last >= 0 {
					hist[i-last]++
				}
				last = i
				prev = cur
			}
		}
		fmt.Printf("n=%d th=%d ptp=%d..%d\ngap histogram: %v\n", n, th, lo, hi, hist)
		return
	}

	var r decode.Result
	switch proto {
	case "manchester":
		r = decode.DecodeManchester(c1, sampleS, decode.ManchesterCfg{IEEE: true, Format: "hex"})
	case "uart":
		baud := 115200 // generator is 8N1 115200; auto-baud is unreliable on ringy edges
		if flag.NArg() > 1 {
			fmt.Sscanf(flag.Arg(1), "%d", &baud)
		}
		r = decode.DecodeUART(c1, sampleS, decode.UARTCfg{Baud: baud, Format: "hex"})
	case "sent":
		r = decode.DecodeSENT(c1, sampleS, decode.SENTCfg{Nibbles: 4}) // generator: status+2data+crc
	case "can":
		r = decode.DecodeCANFD(c1, sampleS, decode.CANFDCfg{DominantLow: true})
	case "mil1553":
		r = decode.DecodeMIL1553(c1, sampleS, decode.MIL1553Cfg{})
	case "arinc429":
		r = decode.DecodeARINC429(c1, sampleS, decode.ARINC429Cfg{})
	case "usbls", "usb":
		proto = "usbls"
		r = decode.DecodeUSBLS(c1, sampleS, decode.USBLSCfg{})
	case "flexray":
		r = decode.DecodeFlexRay(c1, sampleS, decode.FlexRayCfg{})
	case "spi":
		r = decode.DecodeSPI(c1, c2, sampleS, decode.SPICfg{Format: "hex"})
	case "i2c":
		r = decode.DecodeI2C(c1, c2, sampleS, decode.I2CCfg{Format: "hex"})
	default:
		fmt.Println("unknown proto", proto)
		return
	}
	fmt.Printf("proto=%s cols=%d sample_s=%.2gns ptp=%d..%d(%d)\n", proto, n, sampleS*1e9, lo, hi, hi-lo)
	fmt.Printf("  ok=%v decoded_proto=%q err=%q spb=%.1f baud=%d nbytes=%d\n", r.OK, r.Proto, r.Error, r.SPB, r.Baud, len(r.Bytes))
	fmt.Printf("  bytes=% X\n  text=%q\n", r.Bytes, trunc(r.Text, 90))
	exp := expect[proto]
	if len(exp) > 0 {
		hit := contains(r.Bytes, exp)
		verdict := "FAIL"
		if hit && r.OK {
			verdict = "PASS"
		} else if hit {
			verdict = "PASS(bytes, ok=false)"
		}
		fmt.Printf("  EXPECT % X -> %s\n", exp, verdict)
	}
}

func trunc(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return strings.TrimSpace(s)
}
