// ENGMODEL-OWNER-UNIT: FU-APP-BUS
package bus

import (
	"fmt"
	"strings"
	"testing"

	"open-sds/app/internal/sramcapture"
)

// A register read through the worker: the app's per-register round trip.
// TRLC-LINKS: REQ-SDS-001, REQ-SDS-013
func BenchmarkWorkerRead(b *testing.B) {
	d, _ := workerFixture(b)
	for b.Loop() {
		if _, err := d.Read(1, 13); err != nil {
			b.Fatal(err)
		}
	}
}

// TRLC-LINKS: REQ-SDS-081
func BenchmarkBurstIntoWorker(b *testing.B) {
	d, _ := workerFixture(b)
	c1, c2 := make([]uint8, 4096), make([]uint8, 4096)
	for b.Loop() {
		d.BurstInto(c1, c2, 4096)
	}
}

// TRLC-LINKS: REQ-SDS-130
func BenchmarkCheckExact(b *testing.B) {
	w := make([]uint16, 65536)
	for i := range w {
		w[i] = uint16(i * 7)
	}
	ref := append([]uint16(nil), w...)
	var r DrainResult
	r.After.Pops = uint16(len(w))
	for b.Loop() {
		CheckExact(r, w, 0, ref)
	}
}

// TRLC-LINKS: REQ-SDS-133
func BenchmarkMaskToMB(b *testing.B) {
	n := 0
	for b.Loop() {
		for m := uint32(0); m < 16; m++ {
			n += maskToMB(m)
		}
	}
	_ = n
}

// TRLC-LINKS: REQ-SDS-132
func BenchmarkSaveTimingUnchanged(b *testing.B) {
	path := b.TempDir() + "/t.json"
	t := CS1Timing{Config2: 0x00140a00, Config4: 0x10040a04, Config5: 0x0108140a, Config6: 0x00000580}
	f := TimingFile{AppVersion: "v1", Factory: t, Chosen: t}
	for b.Loop() {
		if err := SaveTiming(path, f); err != nil {
			b.Fatal(err)
		}
	}
}

// TRLC-LINKS: REQ-SDS-131
func BenchmarkTimingString(b *testing.B) {
	t := CS1Timing{Config2: 0x00140a00, Config4: 0x10040a04, Config5: 0x0108140a, Config6: 0x00000580}
	for b.Loop() {
		_ = t.String()
	}
}

// TRLC-LINKS: REQ-SDS-081
func BenchmarkDcinvMajor(b *testing.B) {
	devs := "Character devices:\n  1 mem\n  4 /dev/vc/0\n  4 tty\n  5 /dev/tty\n 10 misc\n 13 input\n 29 fb\n 89 i2c\n 90 mtd\n116 alsa\n128 ptm\n136 pts\n153 spi\n180 usb\n189 usb_device\n245 dcinv\n246 fpga_key\n247 Gpmc\n\nBlock devices:\n  1 ramdisk\n  8 sd\n 31 mtdblock\n"
	e := dcinvEnv{devices: func() (string, error) { return devs, nil }}
	for b.Loop() {
		if m, ok := dcinvMajor(e); !ok || m != 245 {
			b.Fatal(m, ok)
		}
	}
	_ = strings.TrimSpace
}

// leWordsRef is the word-at-a-time loop leWords replaces.
// TRLC-LINKS: REQ-SDS-081
func leWordsRef(dst []uint16, src []byte) {
	for i := range dst {
		dst[i] = uint16(src[i*2]) | uint16(src[i*2+1])<<8
	}
}

// leWords reads the bytes as the loop did.
// TRLC-LINKS: REQ-SDS-081
func TestLEWordsIsTheLoop(t *testing.T) {
	src := make([]byte, 2*1000)
	for i := range src {
		src[i] = byte(i*31 + 7)
	}
	a, b := make([]uint16, 1000), make([]uint16, 1000)
	leWords(a, src)
	leWordsRef(b, src)
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("word %d: %#x, want %#x", i, a[i], b[i])
		}
	}
}

// TRLC-LINKS: REQ-SDS-081
func BenchmarkLEWords(b *testing.B) {
	src, dst := make([]byte, 2*8192), make([]uint16, 8192)
	for b.Loop() {
		leWords(dst, src)
	}
}

// TRLC-LINKS: REQ-SDS-081
func BenchmarkLEWordsRef(b *testing.B) {
	src, dst := make([]byte, 2*8192), make([]uint16, 8192)
	for b.Loop() {
		leWordsRef(dst, src)
	}
}

// String reads as the fmt format did.
// TRLC-LINKS: REQ-SDS-131
func TestTimingStringText(t *testing.T) {
	tm := CS1Timing{Config2: 0x00140a00, Config4: 0x10040a04, Config5: 0x0108140a, Config6: 0x00000580}
	want := fmt.Sprintf("rdcycle=%d rdaccess=%d oe=%d..%d cs=%d..%d gap=%d", tm.RdCycle(), tm.RdAccess(),
		tm.OEOn(), tm.OEOff(), tm.CSOn(), tm.CSRdOff(), tm.Gap())
	if got := tm.String(); got != want {
		t.Fatalf("%q, want %q", got, want)
	}
}

// One pump of 512 queued events into the worker's ring.
// TRLC-LINKS: REQ-SDS-013
func BenchmarkWorkerPump(b *testing.B) {
	f := &workerFake{}
	capture, err := sramcapture.New(f)
	if err != nil {
		b.Fatal(err)
	}
	w := &acquisitionWorker{bus: f, capture: capture, ring: make([]byte, workerQueueEvents*32)}
	if err := w.stream(true); err != nil {
		b.Fatal(err)
	}
	f.active.Store(1)
	for b.Loop() {
		f.remaining.Store(512)
		w.pump()
		if w.fault != nil {
			b.Fatal(w.fault)
		}
		w.head, w.count = 0, 0
	}
}
