// srambench accesses only volatile FPGA configuration and bench CS1 registers.
// ENGMODEL-OWNER-UNIT: FU-APP-ACQSRAM
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"open-sds/app/internal/analog"
	"open-sds/app/internal/bus"
	"open-sds/app/internal/cal"
	"open-sds/app/internal/fpgaload"
	"open-sds/app/internal/lcd"
	"open-sds/app/internal/sramcapture"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

var fd int

// TRLC-LINKS: REQ-SDS-170
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

// TRLC-LINKS: REQ-SDS-170
func rd(s uint16) uint16 { return xfer(1, s, 0, false) }

// TRLC-LINKS: REQ-SDS-170
func wr(s, v uint16) { xfer(1, s, v, true) }

// TRLC-LINKS: REQ-SDS-170
func r32(s uint16) uint32 { return uint32(rd(s)) | uint32(rd(s+1))<<16 }

// TRLC-LINKS: REQ-SDS-170
type port struct{}

// TRLC-LINKS: REQ-SDS-170
func (port) ReadCfg() (uint16, error) { return xfer(3, 7, 0, false), nil }

// TRLC-LINKS: REQ-SDS-170
func (port) WriteCfg(v uint16) error { xfer(3, 7, v, true); return nil }

// TRLC-LINKS: REQ-SDS-170
func num(s string) uint32 {
	v, e := strconv.ParseUint(s, 0, 32)
	if e != nil {
		panic(e)
	}
	return uint32(v)
}

// TRLC-LINKS: REQ-SDS-170
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
		show := func(stage string, sent, total int) {}
		if fb, err := lcd.OpenFB(); err == nil {
			surface := lcd.NewMemSurface()
			show = func(stage string, sent, total int) { lcd.DrawLoading(surface, stage, sent, total); fb.Present(surface) }
		}
		show("Preparing firmware", 0, 0)
		p := os.Args[2]
		b, e := os.ReadFile(p)
		if e != nil {
			show("Unable to read firmware", 0, 0)
			panic(e)
		}
		e = fpgaload.Reload(port{}, b, fpgaload.Options{BitOrder: fpgaload.BitOrderReverse, Force: true, Attempts: 1, Logf: func(f string, a ...any) { fmt.Fprintf(os.Stderr, f+"\n", a...) }, Progress: func(sent, total int) {
			stage := "Transferring firmware"
			if sent == total {
				stage = "Verifying firmware"
			}
			show(stage, sent, total)
		}})
		if e != nil {
			show("Firmware loading failed", 0, 0)
			panic(e)
		}
		show("Starting acquisition", len(b), len(b))
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
	profile := &profileBus{dev: dev}
	var captureBus sramcapture.Bus = dev
	if os.Args[1] == "profile-recall" || os.Args[1] == "stream-profile" || os.Args[1] == "kernel-stream-profile" {
		captureBus = profile
	}
	capture, err := sramcapture.New(captureBus)
	if err != nil {
		panic(err)
	}
	if os.Getenv("SCOPE_KERNEL_DMA") == "1" {
		if os.Getenv("SCOPE_EDMA") == "1" {
			panic("kernel and userspace DMA are mutually exclusive")
		}
		if err := dev.EnableKernelDMA(); err != nil {
			panic(err)
		}
		defer dev.CloseKernelDMA()
		fmt.Fprintln(os.Stderr, "kernel IRQ-driven DMA enabled")
	}
	if rd(13) >= 8 && os.Getenv("SCOPE_EDMA") == "1" {
		if err := bus.EnsureDcinv(func(f string, a ...any) { fmt.Fprintf(os.Stderr, f+"\n", a...) }); err != nil {
			panic(err)
		}
		if !dev.EnableEDMA(8192, func(f string, a ...any) { fmt.Fprintf(os.Stderr, f+"\n", a...) }) {
			panic("EDMA unavailable")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	must := func(err error) {
		if err != nil {
			panic(err)
		}
	}
	if os.Args[1] == "profile-recall" || os.Args[1] == "stream-profile" || os.Args[1] == "kernel-stream-profile" {
		// Scope timing changes to this diagnostic process, restoring on exit.
		if os.Getenv("SCOPE_PROFILE_RD_CYCLE") != "" {
			port, err := bus.OpenTimingPort()
			must(err)
			defer port.Close()
			old, err := port.Read()
			must(err)
			defer func() { must(dev.CloseKernelDMA()); must(port.Restore(old)) }()
			cycle := num(os.Getenv("SCOPE_PROFILE_RD_CYCLE"))
			access := num(os.Getenv("SCOPE_PROFILE_RD_ACCESS"))
			next := old.WithRdAccess(access).WithRdCycle(cycle).WithOEOff(min(old.OEOff(), cycle)).WithCSRdOff(min(old.CSRdOff(), cycle))
			if os.Getenv("SCOPE_PROFILE_GAP") != "" {
				next = next.WithGap(num(os.Getenv("SCOPE_PROFILE_GAP")))
			}
			if os.Getenv("SCOPE_PROFILE_RD_CYCLE") == "31" {
				next = next.WithOEOff(16).WithCSRdOff(20)
			}
			must(port.Apply(next))
			fmt.Fprintf(os.Stderr, "profile timing: %s -> %s\n", old.String(), next.String())
		}
	}

	switch os.Args[1] {
	case "burst-probe":
		if os.Getenv("SCOPE_PROBE_SLOW") == "1" {
			port, err := bus.OpenTimingPort()
			must(err)
			defer port.Close()
			old, err := port.Read()
			must(err)
			defer func() { must(dev.CloseKernelDMA()); must(port.Restore(old)) }()
			must(port.Apply(old.WithRdCycle(31).WithRdAccess(13).WithOEOff(16).WithCSRdOff(20)))
		}
		op, n := num(os.Args[2]), num(os.Args[3])
		if (op != 2 && op != 7) || n == 0 || n > 4096 || rd(13) < 8 || rd(1)&20 != 20 {
			panic("burst-probe needs a frozen revision >=8, operation 2/7 and 1..4096 words")
		}
		before := r32(8)
		setCount(n)
		command(uint16(op))
		waitReady()
		if uint32(rd(12)) != n {
			panic("short probe buffer")
		}
		indexed := make([]uint32, n)
		for i := range indexed {
			wr(16, uint16(i))
			for j := 0; j < 4; j++ {
				rd(0)
			}
			indexed[i] = r32(17)
		}
		wr(17, 0)
		halves := make([]uint16, 2*n)
		must(dev.PopWordsChecked(25, halves))
		popped := make([]uint32, n)
		for i := range popped {
			popped[i] = uint32(halves[2*i]) | uint32(halves[2*i+1])<<16
		}
		emit(map[string]any{"operation": op, "before": before, "after": r32(8), "origin": r32(10), "start": r32(4), "indexed": indexed, "popped": popped})
		return
	case "kernel-stream-profile":
		log, n := num(os.Args[2]), num(os.Args[3])
		data := make([]byte, 16400)
		check := &counterCheck{}
		must(dev.StartKernelStream(log, 1, n, 25))
		began := time.Now()
		var next uint64
		blocks := 0
		for {
			got, err := dev.ReadKernelStream(data)
			if err == io.EOF {
				break
			}
			if err != nil {
				stats, _ := dev.KernelStreamStats()
				emit(map[string]any{"error": err.Error(), "words": next, "blocks": blocks, "seconds": time.Since(began).Seconds(), "kernel_stats": stats})
				must(err)
			}
			if got < 20 {
				panic("short kernel stream frame")
			}
			first := binary.LittleEndian.Uint64(data[:8])
			words := binary.LittleEndian.Uint32(data[8:12])
			if first != next || words == 0 || int(words)*4+16 != got || binary.LittleEndian.Uint32(data[12:16]) != 0 {
				panic("kernel stream header mismatch")
			}
			_, err = check.Write(data[16:got])
			must(err)
			if check.first != 0 || check.bad != 0 {
				panic(fmt.Sprintf("kernel counter mismatch: %+v", check))
			}
			next += uint64(words)
			blocks++
		}
		if next < uint64(n) {
			panic(fmt.Sprintf("kernel stream ended before target: got %d words, want at least %d", next, n))
		}
		elapsed := time.Since(began).Seconds()
		stats, _ := dev.KernelStreamStats()
		emit(map[string]any{"kernel_stats": stats, "words": next, "bytes": next * 4, "blocks": blocks, "seconds": elapsed, "words_per_second": float64(next) / elapsed, "first": check.first, "last": check.last, "nonconsecutive_words": check.bad})
		return
	case "stream-profile":
		if rd(13) != 11 {
			panic("stream-profile requires experimental revision 11")
		}
		log, n := num(os.Args[2]), num(os.Args[3])
		if log < 8 || log > 20 || n == 0 {
			panic("stream-profile LOG(8..20) WORDS(>0)")
		}
		st, e := capture.Status()
		must(e)
		if !st.Ready || st.Running {
			panic("halt acquisition before stream-profile")
		}
		pollSleep := time.Duration(0)
		if v := os.Getenv("SCOPE_STREAM_POLL_US"); v != "" {
			us := num(v)
			if us > 1000 {
				panic("stream poll sleep exceeds 1000 us")
			}
			pollSleep = time.Duration(us) * time.Microsecond
		}
		streamCopy := bytes.NewBuffer(make([]byte, 0, 16384))
		must(capture.PrepareStream())
		if os.Getenv("SCOPE_STREAM_RT") == "1" {
			restore, e := realtimeDrain()
			must(e)
			defer func() { must(restore()) }()
		}
		wr(19, 3)
		for {
			ss, e := capture.StreamStatus()
			must(e)
			if ss.Enabled {
				break
			}
			must(ctx.Err())
		}
		must(capture.Arm(ctx, sramcapture.Config{Source: sramcapture.Counter, PreWords: sramcapture.Words - 17, PostWords: 17, DecimationLog2: uint8(log)}))
		defer func() { h, c := context.WithTimeout(context.Background(), time.Second); defer c(); _ = capture.Halt(h) }()
		check := &counterCheck{}
		began := time.Now()
		var next uint64
		blocks := 0
		stopped := false
		for {
			if time.Since(began) > 25*time.Second {
				panic("stream wall deadline exceeded")
			}
			streamCopy.Reset()
			block, e := capture.DrainStream(ctx, next, streamCopy)
			if errors.Is(e, sramcapture.ErrNoStreamBlock) {
				if stopped {
					ss, e := capture.StreamStatus()
					must(e)
					if ss.Fault {
						panic("stream fault while stopping")
					}
					if ss.Finished && ss.Ready == 0 {
						break
					}
				}
				must(ctx.Err())
				if pollSleep != 0 {
					must(sleepDrain(pollSleep.Nanoseconds()))
				}
				continue
			}
			if e != nil {
				emit(map[string]any{"error": e.Error(), "next_word": next, "block": block, "seconds": time.Since(began).Seconds(), "profile": profile})
				must(e)
			}
			_, e = check.Write(streamCopy.Bytes())
			must(e)
			next += uint64(block.Words)
			blocks++
			if check.bad != 0 || check.first != 0 {
				panic(fmt.Sprintf("counter stream mismatch: %+v", check))
			}
			if !stopped && next >= uint64(n) {
				must(capture.Halt(ctx))
				stopped = true
			}
		}
		ss, e := capture.StreamStatus()
		must(e)
		elapsed := time.Since(began)
		emit(map[string]any{"words": next, "bytes": next * 4, "blocks": blocks, "seconds": elapsed.Seconds(), "words_per_second": float64(next) / elapsed.Seconds(), "first": check.first, "last": check.last, "nonconsecutive_words": check.bad, "stream": ss, "profile": profile})
		wr(19, 0)
		return
	case "profile-recall":
		off, n := num(os.Args[2]), num(os.Args[3])

		*profile = profileBus{dev: dev}
		check := &counterCheck{}
		hash := sha256.New()
		began := time.Now()
		recall := capture.Recall
		if os.Getenv("SCOPE_RECALL_FORWARD") == "1" {
			recall = capture.RecallForward
		}
		sink := &timedWriter{dst: io.MultiWriter(hash, check)}
		written, err := recall(ctx, off, n, sink)
		must(err)
		elapsed := time.Since(began)
		emit(map[string]any{"bytes": written, "seconds": elapsed.Seconds(), "validation_seconds": sink.elapsed.Seconds(), "recall_seconds": (elapsed - sink.elapsed).Seconds(), "profile": profile,
			"sha256": fmt.Sprintf("%x", hash.Sum(nil)), "first": check.first, "last": check.last, "nonconsecutive_words": check.bad, "breaks": check.breaks})
		return
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
		bias := uint32(0)
		if len(os.Args) > 6 {
			bias = num(os.Args[6])
			if bias > 3 {
				panic("origin diagnostic bias 0..3")
			}
		}
		base := (r32(10) + r32(4) + bias) & 524287
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
		emit(map[string]any{"words": n, "bytes": n * 4, "prefix": prefix, "sha256": fmt.Sprintf("%x", hash.Sum(nil)), "seconds": time.Since(began).Seconds(), "first": check.first, "last": check.last, "nonconsecutive_words": check.bad, "breaks": check.breaks, "path": path})
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
	case "range-both":
		idx := int(num(os.Args[2]))
		if idx < 0 || idx >= len(analog.Detents) {
			panic("invalid range")
		}
		tr, err := analog.NewDev()
		must(err)
		fe := analog.New(tr, nil, cal.Load(func(f string, a ...any) { fmt.Fprintf(os.Stderr, f+"\n", a...) }))
		for ch := 0; ch < 2; ch++ {
			must(fe.SetVdiv(ch, idx))
		}
		time.Sleep(20 * time.Millisecond)
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
		decim := uint32(0)
		if len(os.Args) > 6 {
			decim = num(os.Args[6])
		}
		if decim > 20 {
			panic("decimation out of range")
		}
		must(capture.Arm(ctx, sramcapture.Config{DecimationLog2: uint8(decim), Source: source, Pair: uint8(cfg>>1) & 7, PreWords: pre, PostWords: post, Normal: cfg&16 != 0, Falling: cfg&32 != 0, TriggerChannel: uint8(cfg>>6) & 1, TriggerLevel: uint8(threshold)}))
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
		emit(map[string]any{"words": n, "bytes": 4 * n, "sha256": fmt.Sprintf("%x", hash.Sum(nil)), "seconds": time.Since(began).Seconds(), "first": check.first, "last": check.last, "nonconsecutive_words": check.bad, "breaks": check.breaks, "path": path})
		return
	case "status":
	default:
		panic("capture CONFIG PRE POST | force | halt | snapshot [N] | recall /dev/acq-NAME OFFSET WORDS | status")
	}
	m, err := capture.Status()
	must(err)
	emit(map[string]any{"capture": m, "identity": rd(0), "status": rd(1), "length": r32(2), "start": r32(4), "trigger_index": r32(6), "position": r32(8), "origin": r32(10), "buffer_words": rd(12), "revision": rd(13), "map_id": rd(14)})
}

// TRLC-LINKS: REQ-SDS-170
func emit(v any) {
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		panic(e)
	}
	fmt.Println(string(b))
}

// TRLC-LINKS: REQ-SDS-170
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

// TRLC-LINKS: REQ-SDS-170
func waitReady() {
	deadline := time.Now().Add(3 * time.Second)
	for rd(1)&4 == 0 {
		if time.Now().After(deadline) {
			panic("SRAM transport timeout")
		}
		time.Sleep(100 * time.Microsecond)
	}
}

// TRLC-LINKS: REQ-SDS-170
func setCount(n uint32) { wr(8, uint16(n)); wr(9, uint16(n>>16)) }

// Counter diagnostics are independent of the capture backend; ADC records
// naturally have nonconsecutive values and are verified by repeat hashes.
// TRLC-LINKS: REQ-SDS-170
type counterCheck struct {
	words, first, last, bad uint32
	breaks                  [][3]uint32
}

// TRLC-LINKS: REQ-SDS-170
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
			if len(c.breaks) < 32 {
				c.breaks = append(c.breaks, [3]uint32{c.words, c.last + 1, v})
			}
		}
		c.last = v
		c.words++
	}
	return len(data), nil
}

// timedWriter separates diagnostic hashing/verification CPU from transport time.
// TRLC-LINKS: REQ-SDS-170
type timedWriter struct {
	dst     io.Writer
	elapsed time.Duration
}

// TRLC-LINKS: REQ-SDS-170
func (w *timedWriter) Write(p []byte) (int, error) {
	start := time.Now()
	n, e := w.dst.Write(p)
	w.elapsed += time.Since(start)
	return n, e
}
