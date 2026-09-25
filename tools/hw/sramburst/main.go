// sramburst — fire the factory image's SRAM burst and read the bulk ports across it.
//
// This is the experiment the SRAM line of work has been pointing at, and it is only meaningful
// with the FACTORY bitstream loaded — which is what the instrument holds in the window after a
// mains cycle and before our app takes over.
//
// WHAT IT DOES, and why each part is safe:
//
//  1. Reads the five full-width GPMC read ports at selectors 0x30..0x34 (offsets 0x60..0x68).
//     the acq2 analysis branch derives these: they are the only five addresses at which all
//     sixteen GPMC data balls depend on an M9K port-B lane — bulk data rather than status.
//     Reads cannot disturb anything.
//
//  2. Only with --fire, replays the vendor's own arm/fire sequence, byte for byte:
//
//     0x21 = 0x00C0, 0x21 = 0x00C0, 0x57 = 0x0001, 0x57 = 0x0000,
//     0x21 = 0x00C3, 0x21 = 0x00C8
//
//     This is not a construction of ours. It is the sequence captured off the vendor firmware
//     and carried in app/internal/diag/diag.go:861 since long before it was decoded, and the
//     decode agrees with it bit for bit: 0x57's 1->0 pulse clears the two 19-bit counters and
//     0x21 bit 1 (0xC0 -> 0xC3) raises GO. The worst case is that the scope does exactly what
//     it does on its own on every acquisition.
//
//  3. Reads the five ports again and reports before/after and what moved.
//
// SAFETY NOTES, both load-bearing:
//   - the fd is NEVER closed — app/internal/bus/bus.go:65 records that closing it frees the
//     FPGA chip select for the whole process tree.
//   - only selectors 0x21 and 0x57 are ever written, and only on CS1. CS3 is never opened
//     (CS3 selector 0x08 wedges the whole instrument).
//
// Everything it does is undone by a power cycle. It writes no file and touches no flash.
//
// Additional bench controls (vendor ARM process must be suspended):
//   --pulse-us=N: reset/fire, wait 0..100000 us, then halt. Logged duration excludes
//     the RUN and HALT ioctl overhead; use the external counter as a second witness.
//   --run: reset/fire and LEAVE RUN ASSERTED; follow with --halt when finished.
//   --refine: issue the factory 0x21=C4 opcode before reading selected ports.
// ENGMODEL-OWNER-UNIT: FU-TOOLS-HW-SRAMBURST
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

const (
	node     = "/dev/Gpmc"
	reqRead  = 0x80026700
	reqWrite = 0x40026701
	planeCS1 = 1
)

// the five full-width read ports, from the acq2 analysis branch  Overridable from argv so a
// CONTROL sweep can be taken: if addresses the map calls STATUS return the same value as the
// bulk ports, we are reading a floating bus, not data, and no conclusion may be drawn.
var ports = []uint16{0x30, 0x31, 0x32, 0x33, 0x34}

// the vendor's own arm/fire words (app/internal/diag/diag.go:861)
var vendor = []struct{ sel, val uint16 }{
	{0x21, 0x00c0}, {0x21, 0x00c0}, {0x57, 0x0001}, {0x57, 0x0000},
	{0x21, 0x00c3}, {0x21, 0x00c8},
}

// findInheritedFD returns the descriptor already open on path, or -1.  fds < 3 are skipped.
// TRLC-LINKS: REQ-SDS-173
func findInheritedFD(path string) int {
	ents, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return -1
	}
	for _, e := range ents {
		n, err := strconv.Atoi(e.Name())
		if err != nil || n < 3 {
			continue
		}
		if t, err := os.Readlink("/proc/self/fd/" + e.Name()); err == nil && t == path {
			return n
		}
	}
	return -1
}

// TRLC-LINKS: REQ-SDS-173
func ioctl(fd int, req uintptr, b *[6]byte) error {
	_, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), req, uintptr(unsafe.Pointer(&b[0])))
	if e != 0 {
		return e
	}
	return nil
}

// TRLC-LINKS: REQ-SDS-173
func rd(fd int, sel uint16) (uint16, error) {
	b := [6]byte{planeCS1, 0, byte(sel), byte(sel >> 8), 0, 0}
	if err := ioctl(fd, reqRead, &b); err != nil {
		return 0, err
	}
	return uint16(b[4]) | uint16(b[5])<<8, nil
}

// TRLC-LINKS: REQ-SDS-173
func wr(fd int, sel, val uint16) error {
	b := [6]byte{planeCS1, 0, byte(sel), byte(sel >> 8), byte(val), byte(val >> 8)}
	return ioctl(fd, reqWrite, &b)
}

// sweep reads every port n times, so a pop-on-read port shows as a moving sequence and a static
// register as a repeated one.
// TRLC-LINKS: REQ-SDS-173
func sweep(fd int, n int) map[string][]uint16 {
	out := map[string][]uint16{}
	for _, s := range ports {
		var vs []uint16
		for i := 0; i < n; i++ {
			v, err := rd(fd, s)
			if err != nil {
				break
			}
			vs = append(vs, v)
		}
		out[fmt.Sprintf("0x%02x", s)] = vs
	}
	return out
}

// TRLC-LINKS: REQ-SDS-173
func main() {
	fire := false
	halt := false
	refine := false
	leaveRunning := false
	timedPulse := false
	pulseUS := int64(0)
	var sel []uint16
	for _, a := range os.Args[1:] {
		if a == "--run" {
			fire, leaveRunning = true, true
			continue
		}
		if a == "--refine" {
			refine = true
			continue
		}
		if strings.HasPrefix(a, "--pulse-us=") {
			v, err := strconv.ParseInt(strings.TrimPrefix(a, "--pulse-us="), 10, 64)
			if err != nil || v < 0 || v > 100000 {
				fmt.Fprintln(os.Stderr, "pulse-us must be 0..100000")
				os.Exit(2)
			}
			pulseUS, fire = v, true
			timedPulse = true
			continue
		}
		if a == "--fire" {
			fire = true
			continue
		}
		if a == "--halt" {
			halt = true
			continue
		}
		if v, err := strconv.ParseUint(a, 0, 16); err == nil {
			sel = append(sel, uint16(v))
		}
	}
	if leaveRunning && timedPulse {
		fmt.Fprintln(os.Stderr, "--run and --pulse-us are mutually exclusive")
		os.Exit(2)
	}
	if len(sel) > 0 {
		ports = sel
	}
	// /dev/Gpmc CANNOT be opened a second time -- a fresh open returns EPERM, because the
	// driver hands the chip select to exactly one opener and that open happens at boot, in
	// init.  app/cmd/app/main.go:316 does the same thing for the same reason: find the
	// INHERITED descriptor by scanning /proc/self/fd.  Children of the OTA agent inherit it as
	// fd 5 (alongside /dev/fpga_key on 6), so a probe launched through the agent has it.
	fd := findInheritedFD(node)
	if fd < 0 {
		fmt.Printf("{\"error\":\"no inherited fd for %s; run this through the OTA agent so it "+
			"is inherited (a fresh open returns EPERM)\"}\n", node)
		os.Exit(1)
	}
	// deliberately never closed: see the header note

	ident := map[string]any{}
	for _, sel := range []uint16{0x18, 0x1c} {
		if v, e := rd(fd, sel); e == nil {
			ident[fmt.Sprintf("0x%02x", sel)] = fmt.Sprintf("0x%04x", v)
		}
	}
	res := map[string]any{
		"node": node, "fired": fire, "identity": ident,
		"ports": []string{"0x30", "0x31", "0x32", "0x33", "0x34"},
	}
	// --halt quiets the FABRIC before the baseline sweep. Suspending the vendor's ARM process is
	// not enough: the FPGA acquires on its own and keeps the data ports moving, which is why a
	// SIGSTOP'd baseline is static in one run and not the next. 0x21 bit 1 low is the vendor's
	// own GO-release, the last word of its sequence, so this is the halt it already issues.
	if halt {
		var hs []string
		for _, w := range []struct{ sel, val uint16 }{{0x21, 0x00c8}, {0x57, 0x0001}, {0x57, 0x0000}} {
			e := wr(fd, w.sel, w.val)
			hs = append(hs, fmt.Sprintf("0x%02x<=0x%04x ok=%v", w.sel, w.val, e == nil))
		}
		res["halt"] = hs
	}
	if refine {
		if e := wr(fd, 0x21, 0x00c4); e != nil {
			fmt.Fprintln(os.Stderr, "refine write failed:", e)
			os.Exit(1)
		}
		res["refine"] = "0x21<=0x00c4 ok=true"
	}
	before := sweep(fd, 8)
	res["before"] = before
	// a second baseline sweep: if before and baseline2 already differ, the bus is not quiet and
	// no conclusion may be drawn from a change across the fire.
	res["baseline2"] = sweep(fd, 8)
	if fire {
		var steps []string
		var runStart time.Time
		for _, w := range vendor {
			if leaveRunning && w.val == 0x00c8 {
				continue
			}
			if w.val == 0x00c8 && timedPulse && !runStart.IsZero() {
				for time.Since(runStart) < time.Duration(pulseUS)*time.Microsecond {
				}
				res["run_to_halt_ns"] = time.Since(runStart).Nanoseconds()
			}
			e := wr(fd, w.sel, w.val)
			if e != nil {
				fmt.Fprintln(os.Stderr, "write failed:", e)
				os.Exit(1)
			}
			if w.val == 0x00c3 && timedPulse {
				runStart = time.Now()
			}
			steps = append(steps, fmt.Sprintf("0x%02x<=0x%04x ok=%v", w.sel, w.val, e == nil))
		}
		res["pulse_us"] = pulseUS
		res["left_running"] = leaveRunning
		res["sequence"] = steps
	}
	after := sweep(fd, 8)
	res["after"] = after

	moved := map[string]bool{}
	for k := range before {
		same := len(before[k]) == len(after[k])
		for i := range before[k] {
			if i >= len(after[k]) || before[k][i] != after[k][i] {
				same = false
			}
		}
		moved[k] = !same
	}
	res["moved"] = moved
	j, _ := json.MarshalIndent(res, "", " ")
	fmt.Println(string(j))
}
