// Package cal loads the per-unit factory calibration (spec 10): the
// scrambled 2752-byte blob at firmdata0/calibration.dat. The factory app's
// in-RAM table does not exist under a clean takeover — the app builds its
// own table from the file, falling back to the redundant backup and then to
// compiled defaults ("Calibration memory lost").
package cal

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
)

const (
	fileSize    = 2752  // 0xac0
	payloadSize = 0xabc // 2748
	numVdiv     = 12    // cal slots vd 0..11 per channel
)

// Rec is one per-(channel, V/div) calibration record (Block A).
type Rec struct {
	GainDAC int16   // fine analog gain code (spidev1.1)
	Zero    int16   // offset-DAC zero (live-zero; file +2 copies to both)
	Gain    float32 // per-decade coefficient — DC-volts diagnostic ONLY,
	// never a render gain (GAIN/Vdiv cancels across detents)
}

// Table is the in-process calibration table.
type Table struct {
	Rec    [2][numVdiv]Rec
	Source string // "file" | "backup" | "defaults"
}

// Default paths on the device.
const (
	PathPrimary = "/usr/bin/siglent/firmdata0/calibration.dat"
	PathBackup  = "/usr/bin/siglent/firmdata0/calibration_bak.dat"
)

// Load reads the cal chain: primary → backup → compiled defaults. It never
// fails; check Source to see what was loaded.
func Load(logf func(string, ...any)) *Table {
	if t, err := LoadFile(PathPrimary); err == nil {
		t.Source = "file"
		logf("cal: loaded %s", PathPrimary)
		return t
	} else {
		logf("cal: %s: %v", PathPrimary, err)
	}
	if t, err := LoadFile(PathBackup); err == nil {
		t.Source = "backup"
		logf("cal: loaded backup %s", PathBackup)
		return t
	} else {
		logf("cal: %s: %v", PathBackup, err)
	}
	logf("cal: CALIBRATION MEMORY LOST — using compiled defaults")
	return Defaults()
}

// LoadFile parses one calibration blob.
func LoadFile(path string) (*Table, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(raw)
}

// Parse validates, de-scrambles and parses a raw 2752-byte blob.
func Parse(raw []byte) (*Table, error) {
	if len(raw) != fileSize {
		return nil, fmt.Errorf("cal: size %d, want %d", len(raw), fileSize)
	}
	// Acceptance is the word-0 checksum ONLY: (sum(payload) + word0) ≡ 0.
	// The self-checksummed tail sub-record is a separate mode-gated path —
	// folding it in rejects otherwise-valid files.
	word0 := binary.LittleEndian.Uint32(raw[0:4])
	var sum uint32
	for _, b := range raw[4:] {
		sum += uint32(b)
	}
	if sum+word0 != 0 {
		return nil, fmt.Errorf("cal: checksum mismatch (sum=%#x word0=%#x)", sum, word0)
	}

	payload := make([]byte, payloadSize)
	copy(payload, raw[4:])
	descramble(payload)

	t := &Table{}
	for ch := 0; ch < 2; ch++ {
		for vd := 0; vd < numVdiv; vd++ {
			base := (ch*numVdiv + vd) * 8
			t.Rec[ch][vd] = Rec{
				GainDAC: int16(binary.LittleEndian.Uint16(payload[base:])),
				Zero:    int16(binary.LittleEndian.Uint16(payload[base+2:])),
				Gain:    math.Float32frombits(binary.LittleEndian.Uint32(payload[base+4:])),
			}
		}
	}
	return t, nil
}

// descramble applies the three byte-involutions IN ORDER (spec 10 §2.2):
// reverse, NOT the back half, NOT the triangular indices. The transform is
// NOT self-inverse — Scramble applies them in reverse order.
func descramble(buf []byte) {
	reverse(buf)
	notBackHalf(buf)
	notTriangular(buf)
}

// Scramble is the write-side inverse (offline tooling/tests only — never
// the live volume).
func Scramble(buf []byte) {
	notTriangular(buf)
	notBackHalf(buf)
	reverse(buf)
}

func reverse(buf []byte) {
	for i, j := 0, len(buf)-1; i < j; i, j = i+1, j-1 {
		buf[i], buf[j] = buf[j], buf[i]
	}
}

func notBackHalf(buf []byte) {
	n := len(buf)
	for i := n - n/2; i < n; i++ {
		buf[i] = ^buf[i]
	}
}

func notTriangular(buf []byte) {
	pos, step := 1, 2
	for pos < len(buf) {
		buf[pos] = ^buf[pos]
		pos += step
		step++
	}
}

// Checksum computes the word-0 value for a scrambled payload (tooling).
func Checksum(payload []byte) uint32 {
	var sum uint32
	for _, b := range payload {
		sum += uint32(b)
	}
	return -sum
}

// Defaults is the compiled fallback table (firmware boot-default ladder).
// Working but uncalibrated; offset zeros at the boot default 10223 (0x27ef).
func Defaults() *Table {
	gainDAC := [numVdiv]int16{0xe6, 0xa8, 0x94, 0x5e, 0x45, 0x20, 0x10, 0x08, 0x0d, 0x1c, 0x48, 0x05}
	// NOTE the per-range break at index 4→5 (0.936 → 16.495): never
	// interpolate this ladder monotonically.
	gain := [numVdiv]float32{19.190, 8.845, 3.801, 1.841, 0.936, 16.495, 8.547, 4.206, 1.719, 0.902, 0.426, 0.169}
	t := &Table{Source: "defaults"}
	for ch := 0; ch < 2; ch++ {
		for vd := 0; vd < numVdiv; vd++ {
			t.Rec[ch][vd] = Rec{GainDAC: gainDAC[vd], Zero: 0x27ef, Gain: gain[vd]}
		}
	}
	return t
}

// DCVolts is the detent-invariant DC diagnostic (spec 10 §3.3):
// (mean − 128) · GAIN / 110. The GAIN coefficient's only consumer.
func (t *Table) DCVolts(ch, vd int, meanCode float64) float64 {
	if ch < 0 || ch > 1 || vd < 0 || vd >= numVdiv {
		return 0
	}
	return (meanCode - 128) * float64(t.Rec[ch][vd].Gain) / 110.0
}
