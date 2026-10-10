// ENGMODEL-OWNER-UNIT: FU-APP-BUS
package bus

import (
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"syscall"
	"time"
	"unsafe"

	"open-sds/app/internal/decodedlines"
	"open-sds/app/internal/sramcapture"
)

const workerQueueEvents = 131072

type workerHardware interface {
	Read(uint8, uint16) (uint16, error)
	RawWrite(uint16, uint16) error
	Write(uint8, uint16, uint16) error
	PopBytesChecked(uint16, []byte) error
	FastDrain() bool
}

type acquisitionWorker struct {
	bus         workerHardware
	capture     *sramcapture.Capture
	deadline    *DecodedDeadline
	active      bool
	epoch       uint32
	fault       error
	head, count int
	ring        []byte
	events      [512]sramcapture.DecodedEvent
	response    [workerHeader + workerPayload]byte
	// lines, when set, receives the drained events instead of the ring: the
	// decoded-stream transcript built here (ADR-STREAM-LINES-IN-WORKER).
	lines      *decodedlines.Transcript
	linesStore *decodedlines.Transcript // reused across streams
	// Stream-bank pump (ADR-STREAM-IMAGE): drained words wait in sring.
	sOn                bool
	sExpected          uint64 // next ordinal to drain
	sHead, sCount      int    // bytes
	sFirst             uint64 // ordinal of the word at sHead
	sFault             error
	sring              []byte
	sLastPump          time.Time     // diagnostics for a lost stream:
	sMaxGap, sMaxDrain time.Duration // longest pump-to-pump gap and bank drain
}

// workerStreamBytes bounds the stream ring: a million words, 30 s of a
// 10 ms/div roll and half a second at the fabric's fastest stream.
const workerStreamBytes = 4 << 20

// TRLC-LINKS: REQ-SDS-035
type streamRingWriter struct{ w *acquisitionWorker }

// TRLC-LINKS: REQ-SDS-035
func (r streamRingWriter) Write(b []byte) (int, error) {
	w := r.w
	if w.sCount+len(b) > len(w.sring) {
		return 0, fmt.Errorf("isolated stream ring overflow; words lost")
	}
	for done := 0; done < len(b); {
		at := (w.sHead + w.sCount) % len(w.sring)
		k := copy(w.sring[at:min(len(w.sring), at+len(b)-done)], b[done:])
		done += k
		w.sCount += k
	}
	return len(b), nil
}

// streamPump drains every published stream bank, in ordinal order.
// TRLC-LINKS: REQ-SDS-035
func (w *acquisitionWorker) streamPump() {
	if !w.sOn || w.sFault != nil {
		return
	}
	now := time.Now()
	if !w.sLastPump.IsZero() {
		w.sMaxGap = max(w.sMaxGap, now.Sub(w.sLastPump))
	}
	w.sLastPump = now
	for i := 0; i < 8; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		t0 := time.Now()
		block, err := w.capture.DrainStream(ctx, w.sExpected, streamRingWriter{w})
		cancel()
		if err == sramcapture.ErrNoStreamBlock {
			return
		}
		if err != nil {
			st, _ := w.capture.StreamStatus()
			w.sFault = fmt.Errorf("%w (after %d words; status %+v; longest pump gap %v, drain %v)", err, w.sExpected, st,
				w.sMaxGap.Round(time.Microsecond), w.sMaxDrain.Round(time.Microsecond))
			return
		}
		w.sMaxDrain = max(w.sMaxDrain, time.Since(t0))
		w.sExpected = block.FirstWord + uint64(block.Words)
	}
}

// streamPumpOn starts or stops the stream pump and its FIFO service.
// TRLC-LINKS: REQ-SDS-035
func (w *acquisitionWorker) streamPumpOn(on bool) error {
	w.sOn, w.sExpected, w.sHead, w.sCount, w.sFirst, w.sFault = on, 0, 0, 0, 0, nil
	w.sLastPump, w.sMaxGap, w.sMaxDrain = time.Time{}, 0, 0
	if on && w.sring == nil {
		w.sring = make([]byte, workerStreamBytes)
	}
	if on && w.deadline == nil {
		if d, ok := w.bus.(*Dev); ok {
			var err error
			if w.deadline, err = d.StartDecodedDeadline(); err != nil {
				return err
			}
		}
	}
	if !on && !w.active && w.deadline != nil {
		if err := w.deadline.Close(); err != nil {
			return err
		}
		w.deadline = nil
	}
	return nil
}

// workerLines bounds the worker's transcript: about a million 8-bit units.
const workerLineCapacity = 65536

// RunAcquisitionWorker is an alternate entry point in the same release binary.
// Only the worker touches GPMC after the parent's startup handshake. Socket EOF
// ends ownership, including when the GUI exits without a graceful shutdown.
// TRLC-LINKS: REQ-SDS-001, REQ-SDS-002, REQ-SDS-013
func RunAcquisitionWorker(gpmcFD, socketFD int, logf func(string, ...any)) error {
	d, err := New(gpmcFD)
	if err != nil {
		return err
	}
	if os.Getenv("SCOPE_EDMA") != "0" && !d.EnableEDMA(8192, logf) {
		return fmt.Errorf("worker requires coherent EDMA")
	}
	c, err := sramcapture.New(d)
	if err != nil {
		return err
	}
	w := &acquisitionWorker{bus: d, capture: c, ring: make([]byte, workerQueueEvents*sramcapture.DecodedEventBytes),
		linesStore: decodedlines.New(0, workerLineCapacity)}
	return w.serve(socketFD)
}

// pump runs independently of GUI requests. The bounded ring can absorb client
// scheduling gaps, but exhaustion is explicit and never masquerades as lossless.
// TRLC-LINKS: REQ-SDS-013
func (w *acquisitionWorker) pump() {
	if !w.active {
		return
	}
	if w.fault != nil {
		_, _ = w.capture.SupportsDecodedCapture()
		return
	}
	events, err := w.capture.ReadDecodedEventsInto(w.epoch, w.events[:])
	if err != nil {
		w.fault = err
		return
	}
	if w.lines != nil {
		w.lines.Add(events)
		return
	}
	for _, event := range events {
		if w.count == workerQueueEvents {
			w.fault = fmt.Errorf("isolated decoded queue overflow; transcript incomplete")
			return
		}
		index := (w.head + w.count) % workerQueueEvents
		if err := event.MarshalTo(w.ring[index*32 : (index+1)*32]); err != nil {
			w.fault = err
			return
		}
		w.count++
	}
}

// TRLC-LINKS: REQ-SDS-001, REQ-SDS-013
func (w *acquisitionWorker) stream(enabled bool) error {
	w.lines = nil
	w.active = false
	w.head = 0
	w.count = 0
	w.fault = nil
	if w.deadline != nil && !w.sOn {
		if err := w.deadline.Close(); err != nil {
			return err
		}
		w.deadline = nil
	}
	if !enabled {
		return nil
	}
	lo, err := w.bus.Read(1, 61)
	if err != nil {
		return err
	}
	hi, err := w.bus.Read(1, 62)
	if err != nil {
		return err
	}
	w.epoch = uint32(lo) | uint32(hi)<<16
	if d, ok := w.bus.(*Dev); ok {
		w.deadline, err = d.StartDecodedDeadline()
		if err != nil {
			return err
		}
	}
	w.active = true
	return nil
}

// handle executes one bounded hardware operation. The response buffer is held
// until its socket write succeeds; a blocked GUI never blocks pump.
// TRLC-LINKS: REQ-SDS-001, REQ-SDS-013
func (w *acquisitionWorker) handle(request []byte) int {
	copy(w.response[:workerHeader], request)
	w.response[10] = 0
	op := binary.LittleEndian.Uint16(request[8:10])
	plane := request[10]
	sel := binary.LittleEndian.Uint16(request[12:14])
	value := binary.LittleEndian.Uint16(request[14:16])
	count := int(binary.LittleEndian.Uint32(request[16:20]))
	epoch := binary.LittleEndian.Uint32(request[20:24])
	data := w.response[workerHeader:]
	n := 0
	var err error
	if count > workerPayload {
		err = fmt.Errorf("oversized worker request")
	} else {
		switch op {
		case workerHello:
			if count != 1 {
				err = fmt.Errorf("invalid ready request")
				break
			}
			if w.bus.FastDrain() {
				data[0] = 1
			} else {
				data[0] = 0
			}
			n = 1
		case workerRead:
			if count != 2 || !validPlane(plane) || sel > 127 {
				err = fmt.Errorf("invalid register read")
				break
			}
			if w.active && plane == 1 && sel == 59 {
				err = fmt.Errorf("decoded event port belongs to worker")
				break
			}
			var v uint16
			v, err = w.bus.Read(plane, sel)
			binary.LittleEndian.PutUint16(data, v)
			n = 2
		case workerWrite:
			if count != 0 || !validPlane(plane) || sel > 127 || (plane == 3 && !Writable(plane, sel)) {
				err = fmt.Errorf("invalid register write")
				break
			}
			if w.active && plane == 1 && (sel == 60 || sel == 76) {
				err = fmt.Errorf("decoded FIFO control belongs to worker")
				break
			}
			if plane == 1 {
				err = w.bus.RawWrite(sel, value)
			} else {
				err = w.bus.Write(plane, sel, value)
			}
			if err == nil && plane == 1 && sel == 57 {
				err = w.stream(value&1 != 0)
			}
		case workerPop:
			if count == 0 || count%2 != 0 || sel > 127 || (w.active && sel == 59) {
				err = fmt.Errorf("invalid or worker-owned pop")
				break
			}
			err = w.bus.PopBytesChecked(sel, data[:count])
			n = count
		case workerEvents:
			if count == 0 || count%32 != 0 || !w.active || epoch != w.epoch {
				err = fmt.Errorf("inactive or stale decoded epoch")
				break
			}
			if w.fault != nil {
				err = w.fault
				break
			}
			amount := min(count/32, w.count)
			first := min(amount, workerQueueEvents-w.head)
			copy(data[:first*32], w.ring[w.head*32:(w.head+first)*32])
			copy(data[first*32:amount*32], w.ring[:(amount-first)*32])
			w.head = (w.head + amount) % workerQueueEvents
			w.count -= amount
			n = amount * 32
		case workerLineMode:
			if !w.active || count != 0 {
				err = fmt.Errorf("line mode needs an active decoded stream")
				break
			}
			if value == 0 {
				w.lines = nil
				break
			}
			if w.linesStore == nil { // first use: the one allocation, before any streaming load
				w.linesStore = decodedlines.New(int(value), workerLineCapacity)
			}
			w.linesStore.Reset(int(value))
			w.lines = w.linesStore
			// Events drained before the switch go into the transcript too.
			for ; w.count > 0; w.count-- {
				e, perr := sramcapture.ParseDecodedEvent(w.ring[w.head*32 : (w.head+1)*32])
				if perr == nil {
					w.lines.Add([]sramcapture.DecodedEvent{e})
				}
				w.head = (w.head + 1) % workerQueueEvents
			}
		case workerLines:
			if !w.active || w.lines == nil || epoch != w.epoch {
				err = fmt.Errorf("no decoded line stream")
				break
			}
			if w.fault != nil {
				err = w.fault
				break
			}
			if plane&1 != 0 {
				w.lines.Flush()
			}
			n = w.lines.EncodeLines(int(sel)<<16|int(value), data[:count])
		case workerStreamPump:
			if count != 0 {
				err = fmt.Errorf("invalid stream pump request")
				break
			}
			err = w.streamPumpOn(value&1 != 0)
		case workerStreamWords:
			if count < 8 || !w.sOn {
				err = fmt.Errorf("no stream pump")
				break
			}
			if w.sFault != nil {
				err = w.sFault
				break
			}
			amount := min(count-8, w.sCount) &^ 3
			binary.LittleEndian.PutUint64(data[:8], w.sFirst)
			for done := 0; done < amount; {
				k := copy(data[8+done:8+amount], w.sring[w.sHead:min(len(w.sring), w.sHead+amount-done)])
				done += k
				w.sHead = (w.sHead + k) % len(w.sring)
			}
			w.sCount -= amount
			w.sFirst += uint64(amount / 4)
			n = 8 + amount
		default:
			err = fmt.Errorf("unknown worker operation")
		}
	}
	if err != nil {
		w.response[10] = 1
		n = copy(data, err.Error())
	}
	binary.LittleEndian.PutUint32(w.response[16:20], uint32(n))
	return workerHeader + n
}

// serve polls a local packet socket with a 500 us deadline. Commands wake it
// immediately; absent commands still leave the FPGA drain independent of Go's
// GUI goroutines. All packet and event buffers are allocated before streaming.
// TRLC-LINKS: REQ-SDS-001, REQ-SDS-013
func (w *acquisitionWorker) serve(fd int) error {
	if err := syscall.SetNonblock(fd, true); err != nil {
		return err
	}
	// Room for several full event packets; the kernel caps it at wmem_max.
	_ = syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_SNDBUF, 512*1024)
	defer func() {
		if w.active {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_ = w.capture.Halt(ctx)
			_, _ = w.capture.EnableDecodedEvents(false)
		}
		if w.deadline != nil {
			_ = w.deadline.Close()
		}
	}()
	var request [workerHeader + 1]byte
	pending := 0
	for {
		w.pump()
		w.streamPump()
		if pending != 0 {
			n, _, errno := syscall.RawSyscall(syscall.SYS_WRITE, uintptr(fd), uintptr(unsafe.Pointer(&w.response[0])), uintptr(pending))
			if errno == 0 {
				if int(n) != pending {
					return fmt.Errorf("short worker response")
				}
				pending = 0
			} else if errno != syscall.EAGAIN && errno != syscall.EINTR && errno != syscall.ENOBUFS {
				// ENOBUFS is transient on a packet socket: the kernel could not
				// buffer this response yet. Exiting on it killed the app's bus
				// at 1.5 MBd (bench 2026-10-06); the next iteration retries.
				return errno
			}
		}
		if pending == 0 {
			n, _, errno := syscall.RawSyscall(syscall.SYS_READ, uintptr(fd), uintptr(unsafe.Pointer(&request[0])), uintptr(len(request)))
			if errno == 0 {
				if n == 0 {
					return nil
				}
				if int(n) != workerHeader || binary.LittleEndian.Uint32(request[:4]) != workerMagic || request[11] != 1 {
					return fmt.Errorf("invalid worker request framing")
				}
				pending = w.handle(request[:workerHeader])
				// Answer before the next FPGA drain: a pump between request and
				// reply put a FIFO read into every register round trip and held
				// a decoded stream below 921600 Bd (bench 2026-10-06).
				n, _, errno := syscall.RawSyscall(syscall.SYS_WRITE, uintptr(fd), uintptr(unsafe.Pointer(&w.response[0])), uintptr(pending))
				if errno == 0 {
					if int(n) != pending {
						return fmt.Errorf("short worker response")
					}
					pending = 0
				} else if errno != syscall.EAGAIN && errno != syscall.EINTR && errno != syscall.ENOBUFS {
					return errno
				}
				continue
			} else if errno != syscall.EAGAIN && errno != syscall.EINTR {
				return errno
			}
		}
		poll := struct {
			FD              int32
			Events, Revents int16
		}{FD: int32(fd), Events: 1}
		if pending != 0 {
			poll.Events = 4
		}
		timeout := syscall.NsecToTimespec(500000)
		_, _, errno := syscall.RawSyscall6(syscall.SYS_PPOLL, uintptr(unsafe.Pointer(&poll)), 1, uintptr(unsafe.Pointer(&timeout)), 0, 0, 0)
		if errno != 0 && errno != syscall.EINTR {
			return errno
		}
		if poll.Revents&(8|16|32) != 0 {
			return nil
		}
	}
}
