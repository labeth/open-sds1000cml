// deepdrain — co-opt the FACTORY fabric to read the external SRAM under our control.
// Run with factory.rbf loaded (fpga_reload factory.rbf, NO -bitrev) and the app STOPPED.
// Configures the analog front end (relay/gain/offset — the loader disturbs the gain on
// spidev1.1, so we re-write it), drives the vendor arm->capture->halt->drain sequence, and
// drains DEEP to test whether the vendor fabric captures past its usual 20480 ceiling.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"open-sds/app/internal/analog"
	"open-sds/app/internal/bus"
	"open-sds/app/internal/cal"
	"open-sds/app/internal/iface"
)

// findFD locates an inherited open fd pointing at path (the OTA agent passes /dev/Gpmc).
func findFD(path string) int {
	es, _ := os.ReadDir("/proc/self/fd")
	for _, e := range es {
		fd, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		if t, err := os.Readlink(filepath.Join("/proc/self/fd", e.Name())); err == nil && t == path {
			return fd
		}
	}
	return -1
}

const (
	selArm      = 0x21
	selClass    = 0x19
	selDivLo    = 0x1a
	selDivHi    = 0x1b
	selRunWord  = 0x35
	selReset2   = 0x36
	selResetHd  = 0x44
	selWrPtr    = 0x57
	selFill     = 0x46
	selStatus   = 0x39
	opResetHead = 0x00c0
	opGo        = 0x00c3
	opHalt      = 0x00c8
	runAuto     = 0x0001
	fillMask    = 0x07ff
	latchAt     = 0x200
	statDone    = 0x0004
)

func minmax(s []uint8) (int, int) {
	lo, hi := 255, 0
	for _, v := range s {
		if int(v) < lo {
			lo = int(v)
		}
		if int(v) > hi {
			hi = int(v)
		}
	}
	return lo, hi
}

// validDepth: end of the last 256-sample window still showing activity (ptp>=thr).
func validDepth(sig []uint8) int {
	lo, hi := minmax(sig)
	ptp := hi - lo
	if ptp < 8 {
		return 0
	}
	thr := ptp / 8
	last := 0
	for from := 0; from < len(sig); from += 256 {
		to := from + 256
		if to > len(sig) {
			to = len(sig)
		}
		l, h := minmax(sig[from:to])
		if h-l >= thr {
			last = to
		}
	}
	return last
}

var outf *os.File

func say(format string, a ...any) {
	fmt.Fprintf(outf, format+"\n", a...)
	outf.Sync()
}

func main() {
	outf, _ = os.Create("/usr/bin/siglent/usr/media/U-disk0/fpgaflash/deepdrain.out")
	if outf == nil {
		outf = os.Stderr
	}
	defer outf.Close()
	say("deepdrain start")
	depth := 40000
	if len(os.Args) > 1 {
		fmt.Sscan(os.Args[1], &depth)
	}
	fd := findFD("/dev/Gpmc")
	if fd < 0 {
		say("no inherited /dev/Gpmc fd")
		return
	}
	b, err := bus.New(fd)
	if err != nil {
		say("bus.New: %v", err)
		return
	}
	cv, _ := b.Read(iface.CS3, 0x07)
	bid, _ := b.Read(iface.CS1, 0x10)
	say("fd=%d bus ok CONF(cs3:07)=0x%04x buildid(cs1:10)=0x%04x", fd, cv, bid)
	w := func(sel, val uint16) { b.Write(iface.CS1, sel, val) }
	r := func(sel uint16) uint16 { v, _ := b.Read(iface.CS1, sel); return v }

	// ---- analog front end: relay+gain via SPI (loader disturbed spidev1.1 gain), offset via CS3 ----
	dev, err := analog.NewDev()
	if err != nil {
		say("analog.NewDev: %v", err)
		return
	}
	say("analog ok")
	tab := cal.Load(func(string, ...any) {})
	say("cal loaded src=%s", tab.Source)
	fe := analog.New(dev, nil, tab)
	fe.SetVdiv(0, 7) // 500 mV/div both channels -> relay + gain DAC
	fe.SetVdiv(1, 7)
	say("vdiv set")
	// vertical offset DAC = calibrated zero (0 V), CS3 lo/hi, hi self-latches
	// (CS3 offset-DAC writes removed: they corrupted GPMC state; SPI relay/gain routes the input)
	say("analog configured (SPI relay+gain only)")
	time.Sleep(10 * time.Millisecond)

	// ---- bring-up: native-fast (class 0x20, div 0), AUTO ----
	w(selResetHd, 1)
	w(selResetHd, 0)
	w(selRunWord, runAuto)
	w(selReset2, 0)
	time.Sleep(5 * time.Millisecond)

	// ---- arm -> wait -> halt ----
	w(selResetHd, 1)
	w(selResetHd, 0)
	w(selRunWord, runAuto)
	w(selReset2, 0)
	w(selArm, opResetHead)
	w(selArm, opResetHead)
	w(selWrPtr, 1)
	w(selWrPtr, 0)
	time.Sleep(2 * time.Millisecond)
	w(selArm, opGo)
	done := false
	for i := 0; i < 400; i++ {
		s := r(selStatus)
		fill := r(selFill) & fillMask
		if s&statDone != 0 && fill >= latchAt {
			done = true
			break
		}
		time.Sleep(150 * time.Microsecond)
	}
	w(selArm, opHalt)
	say("capture: done=%v status=0x%04x fill=0x%04x", done, r(selStatus), r(selFill)&fillMask)

	// ---- DEEP drain ----
	c1 := make([]uint8, depth)
	c2 := make([]uint8, depth)
	b.BurstInto(c1, c2, depth)

	vd1 := validDepth(c1)
	vd2 := validDepth(c2)
	head := 20480
	if head > depth {
		head = depth
	}
	mn, mx := minmax(c1[:head])
	say("C1 drained=%d validDepth=%d  [0:%d] min=%d max=%d ptp=%d", depth, vd1, head, mn, mx, mx-mn)
	say("C2 validDepth=%d", vd2)
	say("C1 head[0:24]=%v", c1[:24])
	if depth > 22000 {
		tmn, tmx := minmax(c1[20480:depth])
		say("C1 TAIL[20480:%d] min=%d max=%d ptp=%d  <== ptp>8 here means DEEP capture (>20480)!", depth, tmn, tmx, tmx-tmn)
		say("C1 @20470..20490=%v", c1[20470:20490])
		say("C1 @30000..30020=%v", c1[30000:30020])
	}
}
