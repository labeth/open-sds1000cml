package decode

// oracle_i2c_dump_test.go — ORACLE HARNESS for the in-fabric I2C decoder
// (i2c_decode.v). Runs the app's OWN decode.DecodeI2C (the oracle) on the
// clean synthetic 2-channel waveforms produced by the repo's oracleI2CWaves
// generator (oracle_i2c_test.go) and DUMPS machine-readable lines the iverilog
// testbench / bench Verify agent parse:
//   ORACLE I2C THRESH8=<t>          -> scl_thr = sda_thr = t (SEL_DEC_THR)
//   ORACLE I2C BYTES=<hex...>       -> expected drained DATA bytes (flags[1]==0)
//   ORACLE I2C NCODES=<n>
//   ORACLE I2C SCL=<int...>         -> per-column SCL codes for sim replay
//   ORACLE I2C SDA=<int...>         -> per-column SDA codes for sim replay
//
// It asserts nothing (that is oracle_i2c_test.go's job) — it DUMPS. Package
// decode so it reaches the test-only generators with zero reimplementation.
//
// Vector selectable via TXN env: single-write (default), repeated-start,
// nak-address, address-extremes.
//
// Run:
//   cd app && go test ./internal/decode -run TestI2COracleDump -v
//   TXN=repeated-start go test ./internal/decode -run TestI2COracleDump -v

import (
	"math"
	"strconv"
	"strings"
	"testing"
)

func TestI2COracleDump(t *testing.T) {
	const sr = 1_000_000
	which := envDefaultStr("TXN", "single-write")

	var txns []i2cTxn
	switch which {
	case "single-write":
		txns = []i2cTxn{{addr7: 0x24, data: []int{0x00, 0x55, 0xAA, 0xFF, 0x0F, 0xF0, 0x80, 0x01}}}
	case "repeated-start":
		txns = []i2cTxn{
			{addr7: 0x3A, data: []int{0x10}, noStop: true},
			{addr7: 0x3A, read: true, data: []int{0x77, 0x88}, nakLast: true},
		}
	case "nak-address":
		txns = []i2cTxn{{addr7: 0x29, nakAddr: true}}
	case "address-extremes":
		txns = []i2cTxn{
			{addr7: 0x00, data: []int{0x12}},
			{addr7: 0x7F, read: true, data: []int{0x34}, nakLast: true},
		}
	default:
		t.Fatalf("unknown TXN %q", which)
	}

	scl, sda := oracleI2CWaves(sr, 50_000, 0.5, txns)
	sclCodes := bitsToCodes(scl)
	sdaCodes := bitsToCodes(sda)
	r := DecodeI2C(sclCodes, sdaCodes, 1.0/sr, I2CCfg{})
	if !r.OK {
		t.Fatalf("DecodeI2C failed: %s", r.Error)
	}

	t.Logf("ORACLE I2C TXN=%s", which)
	t.Logf("ORACLE I2C THRESH8=%d THR=%.4f SPB=%.4f", int(math.Ceil(r.Thr)), r.Thr, r.SPB)
	t.Logf("ORACLE I2C BYTES=% X", intsToBytes(r.Bytes))
	t.Logf("ORACLE I2C NCODES=%d", len(sclCodes))

	dump := func(name string, codes []uint8) {
		var b strings.Builder
		for i, c := range codes {
			if i > 0 {
				b.WriteByte(' ')
			}
			b.WriteString(strconv.Itoa(int(c)))
		}
		t.Logf("ORACLE I2C %s=%s", name, b.String())
	}
	dump("SCL", sclCodes)
	dump("SDA", sdaCodes)
}
