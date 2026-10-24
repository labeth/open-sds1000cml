// ENGMODEL-OWNER-UNIT: FU-APP-BUS
package bus

import (
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"syscall"
	"time"

	"open-sds/app/internal/sramcapture"
)

const workerMagic = 0x57435141
const workerHeader = 24

// workerPayload bounds one worker packet: 2048 decoded events. At 512 a call
// the parent's pulls cap a decoded stream near 82 k events/s and the
// worker's ring overflows (continuous 921600 Bd UART).
const workerPayload = 65536
const (
	workerHello uint16 = iota
	workerRead
	workerWrite
	workerPop
	workerEvents
	workerLineMode // value: serial protocol id, 0 = off (ADR-STREAM-LINES-IN-WORKER)
	workerLines    // selector:value = first absolute line, plane bit 0 = flush
	// ADR-STREAM-IMAGE: the worker drains the stream banks itself.
	workerStreamPump  // value 1 = start draining from ordinal 0, 0 = stop
	workerStreamWords // payload: 8-byte ordinal of the first word, then words
	// workerRecall: whole reads of the frozen record the request names
	// (sramcapture.Capture.RecallRecord); the request carries its parameters
	// after its header, the record's epoch in the header's.
	workerRecall
	// workerPanel: the front panel's scan frame and counters
	// (sramcapture.Capture.ReadPanel) in one round trip, not 22.
	workerPanel
	// workerReadMany: the registers whose selectors follow the header, 16
	// bits each, of one plane, read in order in one round trip.
	workerReadMany
	// workerWriteMany: CS1 writes, a selector and a value of 16 bits each
	// after the header, made in order in one round trip.
	workerWriteMany
)

// workerRecallParams bounds a workerRecall request's parameters: the
// record's id, then per read (at most sramcapture.RecordReadsMax) the
// window's first word, the bucket's words (0: raw) and the bucket or word
// count, each 32 bits little-endian.
const workerRecallParams = 4 + 12*sramcapture.RecordReadsMax

type workerClient struct {
	mu        sync.Mutex
	conn      net.Conn
	cmd       *exec.Cmd
	done      chan error
	sequence  uint32
	deadline  time.Time // the socket's deadline as last set (callSplit)
	failed    error
	fast      bool
	closeOnce sync.Once
	closeErr  error
	response  [workerHeader + workerPayload]byte
}

// StartWorker transfers GPMC ownership before the engine starts. Dup refers to
// the original inherited open file; neither process opens /dev/Gpmc again.
// The parent never uses its DMA mapping after the worker's ready handshake.
// StartWorker and CloseWorker run on the startup goroutine. Keeping its thread
// locked makes Linux's parent-thread death signal follow the worker's lifetime.
// TRLC-LINKS: REQ-SDS-001, REQ-SDS-002, REQ-SDS-013
func (d *Dev) StartWorker(executable string, logf func(string, ...any)) error {
	if d.remote != nil {
		return fmt.Errorf("bus: worker already owns GPMC")
	}
	if d.kernelDMA != nil {
		return fmt.Errorf("bus: kernel DMA already owns hardware")
	}
	pair, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_SEQPACKET|syscall.SOCK_CLOEXEC, 0)
	if err != nil {
		return err
	}
	parent := os.NewFile(uintptr(pair[0]), "worker-parent")
	child := os.NewFile(uintptr(pair[1]), "worker-child")
	defer parent.Close()
	defer child.Close()
	fd, err := syscall.Dup(d.fd)
	if err != nil {
		return err
	}
	inherited := os.NewFile(uintptr(fd), "inherited-gpmc-duplicate")
	defer inherited.Close()
	conn, err := net.FileConn(parent)
	if err != nil {
		return err
	}
	cmd := exec.Command(executable, "--acquisition-worker")
	cmd.ExtraFiles = []*os.File{inherited, child}
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	runtime.LockOSThread()
	if err = cmd.Start(); err != nil {
		runtime.UnlockOSThread()
		conn.Close()
		return err
	}
	c := &workerClient{conn: conn, cmd: cmd, done: make(chan error, 1)}
	go func() { c.done <- cmd.Wait() }()
	var ready [1]byte
	n, err := c.call(workerHello, 0, 0, 0, 0, ready[:])
	if err != nil || n != 1 {
		conn.Close()
		_ = cmd.Process.Kill()
		<-c.done
		runtime.UnlockOSThread()
		return fmt.Errorf("bus: worker startup failed: %v", err)
	}
	c.fast = ready[0] != 0
	if d.edma != nil && !c.fast {
		conn.Close()
		_ = cmd.Process.Kill()
		<-c.done
		runtime.UnlockOSThread()
		return fmt.Errorf("bus: worker lost qualified DMA capability")
	}
	d.remote = c
	if d.edma != nil {
		d.edma.closeFn()
		d.edma = nil
	}
	logf("bus: acquisition worker pid=%d owns inherited GPMC; fast drain=%v", cmd.Process.Pid, c.fast)
	return nil
}

// CloseWorker must follow engine shutdown. Closing the socket wakes the child;
// no fallback to parent hardware access is permitted, even after a failure.
// TRLC-LINKS: REQ-SDS-001, REQ-SDS-002, REQ-SDS-013
func (d *Dev) CloseWorker() error {
	if d.remote == nil {
		return nil
	}
	c := d.remote
	c.closeOnce.Do(func() {
		_ = c.conn.Close()
		select {
		case c.closeErr = <-c.done:
		case <-time.After(3 * time.Second):
			_ = c.cmd.Process.Kill()
			c.closeErr = <-c.done
		}
		runtime.UnlockOSThread()
	})
	return c.closeErr
}

// call never retries a request with uncertain consumption. A protocol or socket
// failure permanently invalidates this proxy; reported device errors do not.
// TRLC-LINKS: REQ-SDS-001, REQ-SDS-013
func (c *workerClient) call(op uint16, plane uint8, selector, value uint16, epoch uint32, dst []byte) (int, error) {
	return c.callWith(op, plane, selector, value, epoch, nil, dst)
}

// callWith is call with params sent after the request's header.
// TRLC-LINKS: REQ-SDS-001, REQ-SDS-013
func (c *workerClient) callWith(op uint16, plane uint8, selector, value uint16, epoch uint32, params, dst []byte) (int, error) {
	return c.callSplit(op, plane, selector, value, epoch, params, nil, dst)
}

// callSplit is callWith with the reply's first len(head) bytes copied to
// head and the rest to dst: a stream reply's ordinal goes apart from its
// words, which land where the caller keeps them.
// TRLC-LINKS: REQ-SDS-001, REQ-SDS-013, REQ-SDS-035
func (c *workerClient) callSplit(op uint16, plane uint8, selector, value uint16, epoch uint32, params, head, dst []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failed != nil {
		return 0, c.failed
	}
	want := len(head) + len(dst)
	if want > workerPayload {
		return 0, fmt.Errorf("bus: oversized worker response")
	}
	c.sequence++
	if len(params) > workerRecallParams {
		return 0, fmt.Errorf("bus: oversized worker request")
	}
	var buf [workerHeader + workerRecallParams]byte
	request := buf[:workerHeader+copy(buf[workerHeader:], params)]
	binary.LittleEndian.PutUint32(request[:4], workerMagic)
	binary.LittleEndian.PutUint32(request[4:8], c.sequence)
	binary.LittleEndian.PutUint16(request[8:10], op)
	request[10], request[11] = plane, 1
	binary.LittleEndian.PutUint16(request[12:14], selector)
	binary.LittleEndian.PutUint16(request[14:16], value)
	binary.LittleEndian.PutUint32(request[16:20], uint32(want))
	binary.LittleEndian.PutUint32(request[20:24], epoch)
	// The 3 s deadline is moved on only once less than 2 s of it is left:
	// a hung worker still fails a call within 3 s, and a call does not pay
	// for re-arming the poller's timer every time.
	if now := time.Now(); now.After(c.deadline.Add(-2 * time.Second)) {
		c.deadline = now.Add(3 * time.Second)
		_ = c.conn.SetDeadline(c.deadline)
	}
	n, err := c.conn.Write(request)
	if err == nil && n != len(request) {
		err = fmt.Errorf("short worker request")
	}
	if err == nil {
		n, err = c.conn.Read(c.response[:])
	}
	if err == nil && (n < workerHeader || binary.LittleEndian.Uint32(c.response[:4]) != workerMagic || binary.LittleEndian.Uint32(c.response[4:8]) != c.sequence || binary.LittleEndian.Uint16(c.response[8:10]) != op || c.response[11] != 1 || int(binary.LittleEndian.Uint32(c.response[16:20])) != n-workerHeader) {
		err = fmt.Errorf("invalid worker response")
	}
	if err != nil {
		c.failed = fmt.Errorf("bus: worker transport failed (consumption uncertain): %w", err)
		c.conn.Close()
		return 0, c.failed
	}
	if c.response[10] != 0 {
		return 0, fmt.Errorf("bus worker: %s", c.response[workerHeader:n])
	}
	if n-workerHeader > want {
		c.failed = fmt.Errorf("bus: oversized worker payload")
		c.conn.Close()
		return 0, c.failed
	}
	k := copy(head, c.response[workerHeader:n])
	return k + copy(dst, c.response[workerHeader+k:n]), nil
}

// RecallIsolated has the worker make reads of the frozen record (epoch,
// record) names, windows holding each one's first word, bucket words (0: raw)
// and bucket or word count, into dst whole (sramcapture.Capture.RecallRecord):
// one round trip for them all. owned is false without a worker, or when they
// exceed a worker packet.
// TRLC-LINKS: REQ-SDS-001, REQ-SDS-033
func (d *Dev) RecallIsolated(epoch, record uint32, windows []uint32, dst []byte) (bool, error) {
	if d.remote == nil || len(dst) == 0 || len(dst) > workerPayload || len(windows) == 0 || len(windows)%3 != 0 || 4+4*len(windows) > workerRecallParams {
		return false, nil
	}
	var params [workerRecallParams]byte
	binary.LittleEndian.PutUint32(params[:], record)
	for i, v := range windows {
		binary.LittleEndian.PutUint32(params[4+4*i:], v)
	}
	n, err := d.remote.callWith(workerRecall, 0, 0, 0, epoch, params[:4+4*len(windows)], dst)
	if err == nil && n != len(dst) {
		err = fmt.Errorf("bus: short worker recall: %d of %d bytes", n, len(dst))
	}
	return true, err
}

// ReadIsolatedPanel has the worker read the front panel's scan frame and
// counters (sramcapture.Capture.ReadPanel's eleven words, in its order);
// owned is false without a worker. The panel is read 25 times a second.
// TRLC-LINKS: REQ-SDS-022, REQ-SDS-013
func (d *Dev) ReadIsolatedPanel() (words [11]uint16, owned bool, err error) {
	if d.remote == nil {
		return words, false, nil
	}
	var buf [22]byte
	n, err := d.remote.call(workerPanel, 0, 0, 0, 0, buf[:])
	if err == nil && n != len(buf) {
		err = fmt.Errorf("bus: short worker panel read")
	}
	if err != nil {
		return words, true, err
	}
	for i := range words {
		words[i] = binary.LittleEndian.Uint16(buf[2*i:])
	}
	return words, true, nil
}

// ReadIsolatedDecodedBytes bypasses hardware FIFO reads only when this Dev is a
// proxy. The worker returns complete versioned event records from its own ring.
// TRLC-LINKS: REQ-SDS-013
func (d *Dev) ReadIsolatedDecodedBytes(epoch uint32, dst []byte) (int, bool, error) {
	if d.remote == nil {
		return 0, false, nil
	}
	n, err := d.remote.call(workerEvents, 0, 0, 0, epoch, dst)
	return n, true, err
}

// SetIsolatedStreamPump makes the worker drain the stream banks (on) into its
// ring, at its FIFO service, from ordinal 0; owned is false without a worker.
// TRLC-LINKS: REQ-SDS-035
func (d *Dev) SetIsolatedStreamPump(on bool) (bool, error) {
	if d.remote == nil {
		return false, nil
	}
	v := uint16(0)
	if on {
		v = 1
	}
	_, err := d.remote.call(workerStreamPump, 0, 0, v, 0, nil)
	return true, err
}

// ReadIsolatedStream takes drained stream words from the worker's ring into
// dst (whole 32-bit words) and returns the ordinal of the first. A fault or
// ring overflow is an error: the stream lost words.
// TRLC-LINKS: REQ-SDS-035
func (d *Dev) ReadIsolatedStream(dst []byte) (first uint64, n int, owned bool, err error) {
	if d.remote == nil {
		return 0, 0, false, nil
	}
	var head [8]byte
	limit := min(len(dst), workerPayload-8) &^ 3
	m, err := d.remote.callSplit(workerStreamWords, 0, 0, 0, 0, nil, head[:], dst[:limit])
	if err != nil || m < 8 {
		if err == nil {
			err = fmt.Errorf("bus: short worker stream reply")
		}
		return 0, 0, true, err
	}
	return binary.LittleEndian.Uint64(head[:]), m - 8, true, nil
}

// RawWriteMany makes the CS1 writes regs (selector, value) in order, as
// RawWrite each: with a bus worker a round trip for up to thirteen, not one
// each (a decoder's trigger setup writes up to nine).
// TRLC-LINKS: REQ-SDS-001, REQ-SDS-129
func (d *Dev) RawWriteMany(regs [][2]uint16) error {
	if d.remote == nil || len(regs) < 2 {
		for _, r := range regs {
			if err := d.RawWrite(r[0], r[1]); err != nil {
				return err
			}
		}
		return nil
	}
	const per = workerRecallParams / 4
	for len(regs) > 0 {
		n := min(len(regs), per)
		var params [workerRecallParams]byte
		for i, r := range regs[:n] {
			binary.LittleEndian.PutUint16(params[4*i:], r[0])
			binary.LittleEndian.PutUint16(params[4*i+2:], r[1])
		}
		if _, err := d.remote.callWith(workerWriteMany, PlaneCS1, 0, 0, 0, params[:4*n], nil); err != nil {
			return fmt.Errorf("bus: write cs1: %w", err)
		}
		regs = regs[n:]
	}
	return nil
}

// ReadMany reads the registers sels of plane into dst in order: with a bus
// worker in one round trip, not one each (a capture's status reads twenty).
// TRLC-LINKS: REQ-SDS-001, REQ-SDS-129
func (d *Dev) ReadMany(plane uint8, sels, dst []uint16) error {
	if len(dst) < len(sels) {
		return fmt.Errorf("bus: read of %d registers into %d", len(sels), len(dst))
	}
	if d.remote == nil || len(sels) < 2 || 2*len(sels) > workerRecallParams || !validPlane(plane) {
		for i, sel := range sels {
			v, err := d.Read(plane, sel)
			if err != nil {
				return err
			}
			dst[i] = v
		}
		return nil
	}
	var params [workerRecallParams]byte
	for i, sel := range sels {
		binary.LittleEndian.PutUint16(params[2*i:], sel)
	}
	var out [workerRecallParams]byte
	n, err := d.remote.callWith(workerReadMany, plane, 0, 0, 0, params[:2*len(sels)], out[:2*len(sels)])
	if err != nil {
		return fmt.Errorf("bus: read cs%d: %w", plane, err)
	}
	if n != 2*len(sels) {
		return fmt.Errorf("bus: short worker register reads")
	}
	for i := range sels {
		dst[i] = binary.LittleEndian.Uint16(out[2*i:])
	}
	return nil
}

// TRLC-LINKS: REQ-SDS-001, REQ-SDS-013
func (d *Dev) workerIOCTL(req uintptr, b *[6]byte) error {
	sel := binary.LittleEndian.Uint16(b[2:4])
	if req == reqRead {
		n, err := d.remote.call(workerRead, b[0], sel, 0, 0, b[4:6])
		if err == nil && n != 2 {
			return fmt.Errorf("bus: short worker register read")
		}
		return err
	}
	_, err := d.remote.call(workerWrite, b[0], sel, binary.LittleEndian.Uint16(b[4:6]), 0, nil)
	runtime.KeepAlive(b)
	return err
}

// SetIsolatedLineMode makes the worker build the decoded-stream transcript
// for a serial protocol (0 stops it); owned is false without a worker.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
func (d *Dev) SetIsolatedLineMode(proto int) (bool, error) {
	if d.remote == nil {
		return false, nil
	}
	_, err := d.remote.call(workerLineMode, 0, 0, uint16(proto), 0, nil)
	return true, err
}

// ReadIsolatedLines fetches encoded transcript lines from absolute index from
// (decodedlines wire format); flush completes the line in progress first.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
func (d *Dev) ReadIsolatedLines(epoch uint32, from int, flush bool, dst []byte) (int, bool, error) {
	if d.remote == nil {
		return 0, false, nil
	}
	plane := uint8(0)
	if flush {
		plane = 1
	}
	n, err := d.remote.call(workerLines, plane, uint16(from>>16), uint16(from), epoch, dst)
	return n, true, err
}
