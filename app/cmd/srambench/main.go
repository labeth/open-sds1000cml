// srambench accesses only volatile FPGA configuration and bench CS1 registers.
// ENGMODEL-OWNER-UNIT: FU-APP-SRAMBENCH
package main

import (
	"encoding/json"
	"fmt"
	"open-sds/app/internal/fpgaload"
	"os"
	"strconv"
	"syscall"
	"time"
	"unsafe"
)

var fd int

// TRLC-LINKS: REQ-SDS-171
func xfer(plane, sel, val uint16, write bool) uint16 {
	b := [6]byte{byte(plane), 0, byte(sel), byte(sel >> 8), byte(val), byte(val >> 8)}
	req := uintptr(0x80026700)
	if write {
		req = 0x40026701
	}
	_, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), req, uintptr(unsafe.Pointer(&b[0])))
	if e != 0 {
		panic(e)
	}
	return uint16(b[4]) | uint16(b[5])<<8
}
// TRLC-LINKS: REQ-SDS-171
func rd(s uint16) uint16  { return xfer(1, s, 0, false) }
// TRLC-LINKS: REQ-SDS-171
func wr(s, v uint16)      { xfer(1, s, v, true) }
// TRLC-LINKS: REQ-SDS-171
func r32(s uint16) uint32 { return uint32(rd(s)) | uint32(rd(s+1))<<16 }

// TRLC-LINKS: REQ-SDS-171
type port struct{}

// TRLC-LINKS: REQ-SDS-171
func (port) ReadCfg() (uint16, error) { return xfer(3, 7, 0, false), nil }
// TRLC-LINKS: REQ-SDS-171
func (port) WriteCfg(v uint16) error  { xfer(3, 7, v, true); return nil }
// TRLC-LINKS: REQ-SDS-171
func num(s string) uint32 {
	v, e := strconv.ParseUint(s, 0, 32)
	if e != nil {
		panic(e)
	}
	return uint32(v)
}
// TRLC-LINKS: REQ-SDS-171
func main() {
	fd = -1
	es, e := os.ReadDir("/proc/self/fd")
	if e != nil {
		panic(e)
	}
	for _, x := range es {
		n, _ := strconv.Atoi(x.Name())
		v, _ := os.Readlink("/proc/self/fd/" + x.Name())
		if n >= 3 && v == "/dev/Gpmc" {
			fd = n
			break
		}
	}
	if fd < 0 {
		panic("inherited GPMC fd required; never fresh-open or close it")
	}
	if len(os.Args) < 2 {
		panic("load FILE | status | run SEED LATENCY")
	}
	if os.Args[1] == "load" {
		p := os.Args[2]
		b, e := os.ReadFile(p)
		if e != nil {
			panic(e)
		}
		e = fpgaload.Reload(port{}, b, fpgaload.Options{BitOrder: fpgaload.BitOrderReverse, Force: true, Attempts: 1, Logf: func(f string, a ...any) { fmt.Fprintf(os.Stderr, f+"\n", a...) }})
		if e != nil {
			panic(e)
		}
		fmt.Printf("loaded %d bytes, CONF_DONE=%04x, identity=%04x\n", len(b), xfer(3, 7, 0, false), rd(0))
		return
	}
	if rd(0) != 0x5b51 {
		panic(fmt.Sprintf("wrong bench identity %04x", rd(0)))
	}
	if os.Args[1] == "measure" {
		snap := func() (uint32, time.Time, time.Duration) {
			ack := rd(25)
			a := time.Now()
			wr(22, 1)
			for rd(25) == ack {
				if time.Since(a) > time.Second {
					panic("snapshot timeout")
				}
			}
			z := time.Now()
			return r32(23), a.Add(z.Sub(a) / 2), z.Sub(a)
		}
		c0, t0, u0 := snap()
		time.Sleep(2 * time.Second)
		c1, t1, u1 := snap()
		d := map[string]any{"ticks": c1 - c0, "seconds": t1.Sub(t0).Seconds(), "measured_hz": float64(c1-c0) / t1.Sub(t0).Seconds(), "snapshot_uncertainty_ns": []int64{u0.Nanoseconds(), u1.Nanoseconds()}}
		out, _ := json.MarshalIndent(d, "", "  ")
		fmt.Println(string(out))
		return
	}
	runOne := func(seed uint32, delay uint32) {
		if delay > 7 || seed == 0 {
			panic("nonzero seed and latency 0..7 required")
		}
		wr(2, uint16(seed))
		wr(3, uint16(seed>>16))
		wr(4, uint16(delay))
		wr(1, 1)
		time.Sleep(200 * time.Microsecond)
		deadline := time.Now().Add(15 * time.Second)
		for rd(1)&15 != 5 {
			if time.Now().After(deadline) {
				panic("bench timeout")
			}
			time.Sleep(200 * time.Microsecond)
		}
	}
	if os.Args[1] == "stress" {
		seed := num(os.Args[2])
		delay := num(os.Args[3])
		passes := num(os.Args[4])
		if passes == 0 || passes > 10000 {
			panic("passes 1..10000")
		}
		began := time.Now()
		var checked, errs uint64
		var completed uint32
		for completed < passes {
			runOne(seed, delay)
			n, e := r32(8), r32(6)
			if n != 524288 {
				panic("incomplete memory sweep")
			}
			checked += uint64(n)
			errs += uint64(e)
			completed++
			if e != 0 {
				break
			}
			seed ^= seed << 13
			seed ^= seed >> 17
			seed ^= seed << 5
		}
		result := map[string]any{"completed_passes": completed, "requested_passes": passes, "checked_words": checked, "errors": errs, "last_seed": seed, "latency": delay, "elapsed_seconds": time.Since(began).Seconds(), "first_index": r32(10), "first_want": r32(12), "first_got": r32(14), "bad_mask": r32(28)}
		out, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(out))
		return
	}
	if os.Args[1] == "run" {
		runOne(num(os.Args[2]), num(os.Args[3]))
	}

	head := []uint32{}
	for i := 0; i < 32; i++ {
		wr(5, uint16(i))
		head = append(head, r32(16))
	}
	result := map[string]any{"identity": rd(0), "state": rd(1), "seed": r32(2), "latency": rd(4), "errors": r32(6), "checked": r32(8), "first_index": r32(10), "first_want": r32(12), "first_got": r32(14), "cycles": r32(18), "nominal_mhz": rd(20), "revision": rd(21), "bad_mask": r32(28), "head": head}
	out, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(out))
}
