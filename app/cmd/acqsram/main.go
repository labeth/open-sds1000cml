// srambench accesses only volatile FPGA configuration and bench CS1 registers.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"open-sds/app/internal/bus"
	"open-sds/app/internal/fpgaload"
	"open-sds/app/internal/sramcapture"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

var fd int

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
func rd(s uint16) uint16  { return xfer(1, s, 0, false) }
func wr(s, v uint16)      { xfer(1, s, v, true) }
func r32(s uint16) uint32 { return uint32(rd(s)) | uint32(rd(s+1))<<16 }

type port struct{}

func (port) ReadCfg() (uint16, error) { return xfer(3, 7, 0, false), nil }
func (port) WriteCfg(v uint16) error  { xfer(3, 7, v, true); return nil }
func num(s string) uint32 {
	v, e := strconv.ParseUint(s, 0, 32)
	if e != nil {
		panic(e)
	}
	return uint32(v)
}
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
	if rd(0) != 0x5a52 {
		panic(fmt.Sprintf("wrong capture identity %04x", rd(0)))
	}
	dev, err := bus.New(fd)
	if err != nil {
		panic(err)
	}
	capture, err := sramcapture.New(dev)
	if err != nil {
		panic(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	must := func(err error) {
		if err != nil {
			panic(err)
		}
	}
	switch os.Args[1] {
	case "recall-warm":
		path := os.Args[2]
		if !strings.HasPrefix(path, "/dev/acq-") || strings.Contains(path[5:], "/") {
			panic("output must be /dev/acq-NAME (tmpfs)")
		}
		off, n, prefix := num(os.Args[3]), num(os.Args[4]), num(os.Args[5])
		if prefix == 0 || prefix > 64 || n == 0 || rd(1)&20 != 20 || uint64(off)+uint64(n) > uint64(r32(2)) || rd(19)&1 != 0 {
			panic("warm recall requires a valid frozen window and prefix 1..64")
		}
		f, e := os.Create(path)
		must(e)
		defer f.Close()
		hash := sha256.New()
		check := &counterCheck{}
		dst := io.MultiWriter(f, hash, check)
		began := time.Now()
		base := (r32(10) + r32(4)) & 524287
		for done := uint32(0); done < n; {
			chunk := n - done
			if chunk > 512-prefix {
				chunk = 512 - prefix
			}
			target := (base + off + done - prefix) & 524287
			skip := (target - r32(8)) & 524287
			if skip != 0 {
				setCount(skip)
				command(3)
				waitReady()
			}
			setCount(chunk + prefix)
			command(2)
			waitReady()
			buf := make([]byte, chunk*4)
			for i := uint32(0); i < chunk; i++ {
				wr(16, uint16(i+prefix))
				for settle := 0; settle < 4; settle++ {
					rd(0)
				}
				binary.LittleEndian.PutUint32(buf[4*i:], r32(17))
			}
			_, e = dst.Write(buf)
			must(e)
			done += chunk
		}
		must(f.Close())
		emit(map[string]any{"words": n, "bytes": n * 4, "prefix": prefix, "sha256": fmt.Sprintf("%x", hash.Sum(nil)), "seconds": time.Since(began).Seconds(), "first": check.first, "last": check.last, "nonconsecutive_words": check.bad, "path": path})
		return
	case "readtrace":
		emit(map[string]any{"first": r32(20), "second": r32(22), "third_low": rd(24)})
		return
	case "encode-mask":
		mask := num(os.Args[2])
		if mask > 1023 || rd(13) < 6 || rd(1)&8 != 0 {
			panic("encode mask requires idle interleave image and 10-bit mask")
		}
		wr(15, uint16(mask))
		emit(map[string]any{"encode_mask": mask})
	case "physical":
		off, n := num(os.Args[2]), num(os.Args[3])
		if off >= 524288 || n == 0 || n > 512 || rd(1)&20 != 20 {
			panic("physical probe needs frozen record, offset 0..524287, count 1..512")
		}
		target := (r32(10) + off) & 524287
		skip := (target - r32(8)) & 524287
		if skip != 0 {
			setCount(skip)
			command(3)
			waitReady()
		}
		setCount(n)
		command(2)
		waitReady()
		values := make([]uint32, n)
		for i := range values {
			wr(16, uint16(i))
			// Let the synchronized index write and registered M9K read retire.
			for settle := 0; settle < 4; settle++ {
				rd(0)
			}
			values[i] = r32(17)
		}
		emit(map[string]any{"offset": off, "values": values})
		return
	case "offset":
		ch, code := num(os.Args[2]), num(os.Args[3])
		if ch > 1 || code < 10000 || code > 11000 {
			panic("DC probe requires channel 0/1 and offset DAC code 10000..11000")
		}
		// Existing acq2 MAX V offset-DAC sequence: low byte then latching high.
		// These are volatile DAC registers, not nonvolatile configuration.
		xfer(3, 0x10+uint16(ch), uint16(code)&255, true)
		xfer(3, 0x30+uint16(ch), uint16(code)>>8, true)
		time.Sleep(20 * time.Millisecond)
	case "capture":
		cfg, pre, post := num(os.Args[2]), num(os.Args[3]), num(os.Args[4])
		if cfg > 255 || (cfg>>1)&7 > 4 || post == 0 || uint64(pre)+uint64(post) > 524288 {
			panic("invalid capture geometry/config")
		}
		threshold := uint32(128)
		if len(os.Args) > 5 {
			threshold = num(os.Args[5])
		}
		if threshold > 255 {
			panic("trigger code 0..255")
		}
		source := sramcapture.Counter
		if cfg&1 != 0 {
			source = sramcapture.ADC
		}
		must(capture.Arm(ctx, sramcapture.Config{Source: source, Pair: uint8(cfg>>1) & 7, PreWords: pre, PostWords: post, Normal: cfg&16 != 0, Falling: cfg&32 != 0, TriggerChannel: uint8(cfg>>6) & 1, TriggerLevel: uint8(threshold)}))
		if cfg&16 == 0 {
			must(capture.WaitFrozen(ctx))
		}
	case "force":
		must(capture.Force(ctx))
		must(capture.WaitFrozen(ctx))
	case "halt":
		must(capture.Halt(ctx))
		must(capture.WaitFrozen(ctx))
	case "snapshot":
		n := 1
		if len(os.Args) > 2 {
			n = int(num(os.Args[2]))
		}
		if n < 1 || n > 4096 {
			panic("snapshot count 1..4096")
		}
		samples := make([][10]byte, n)
		for i := range samples {
			var err error
			samples[i], err = capture.Snapshot(ctx)
			must(err)
		}
		emit(map[string]any{"cores": samples})
		return
	case "recall":
		// Output only to explicitly permitted volatile scope storage.
		path := os.Args[2]
		if !strings.HasPrefix(path, "/dev/acq-") || strings.Contains(path[5:], "/") {
			panic("output must be /dev/acq-NAME (tmpfs)")
		}
		off, n := num(os.Args[3]), num(os.Args[4])
		total := r32(2)
		if rd(1)&20 != 20 || uint64(off)+uint64(n) > uint64(total) {
			panic("record not frozen or recall out of bounds")
		}
		f, e := os.Create(path)
		if e != nil {
			panic(e)
		}
		defer f.Close()
		hash := sha256.New()
		began := time.Now()
		check := &counterCheck{}
		written, err := capture.Recall(ctx, off, n, io.MultiWriter(f, hash, check))
		must(err)
		if written != int64(n)*4 {
			panic("short full-record recall")
		}
		if e = f.Close(); e != nil {
			panic(e)
		}
		emit(map[string]any{"words": n, "bytes": 4 * n, "sha256": fmt.Sprintf("%x", hash.Sum(nil)), "seconds": time.Since(began).Seconds(), "first": check.first, "last": check.last, "nonconsecutive_words": check.bad, "path": path})
		return
	case "status":
	default:
		panic("capture CONFIG PRE POST | force | halt | snapshot [N] | recall /dev/acq-NAME OFFSET WORDS | status")
	}
	m, err := capture.Status()
	must(err)
	emit(map[string]any{"capture": m, "identity": rd(0), "status": rd(1), "length": r32(2), "start": r32(4), "trigger_index": r32(6), "position": r32(8), "origin": r32(10), "buffer_words": rd(12), "revision": rd(13), "map_id": rd(14)})
}
func emit(v any) {
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		panic(e)
	}
	fmt.Println(string(b))
}
func command(op uint16) {
	ack := rd(1) & 512
	wr(1, op)
	deadline := time.Now().Add(time.Second)
	for rd(1)&512 == ack {
		if time.Now().After(deadline) {
			panic("command ACK timeout")
		}
	}
	if rd(1)&384 != 0 {
		panic("capture command rejected")
	}
}
func waitReady() {
	deadline := time.Now().Add(3 * time.Second)
	for rd(1)&4 == 0 {
		if time.Now().After(deadline) {
			panic("SRAM transport timeout")
		}
		time.Sleep(100 * time.Microsecond)
	}
}
func setCount(n uint32) { wr(8, uint16(n)); wr(9, uint16(n>>16)) }

// Counter diagnostics are independent of the capture backend; ADC records
// naturally have nonconsecutive values and are verified by repeat hashes.
type counterCheck struct{ words, first, last, bad uint32 }

func (c *counterCheck) Write(data []byte) (int, error) {
	if len(data)%4 != 0 {
		return 0, fmt.Errorf("unaligned counter diagnostic input")
	}
	for i := 0; i < len(data); i += 4 {
		v := binary.LittleEndian.Uint32(data[i:])
		if c.words == 0 {
			c.first = v
		} else if v != c.last+1 {
			c.bad++
		}
		c.last = v
		c.words++
	}
	return len(data), nil
}
