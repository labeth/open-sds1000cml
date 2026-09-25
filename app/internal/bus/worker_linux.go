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
)

const workerMagic = 0x57435141
const workerHeader = 24
const workerPayload = 16384
const (
	workerHello uint16 = iota
	workerRead
	workerWrite
	workerPop
	workerEvents
)

type workerClient struct {
	mu        sync.Mutex
	conn      net.Conn
	cmd       *exec.Cmd
	done      chan error
	sequence  uint32
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
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failed != nil {
		return 0, c.failed
	}
	if len(dst) > workerPayload {
		return 0, fmt.Errorf("bus: oversized worker response")
	}
	c.sequence++
	var request [workerHeader]byte
	binary.LittleEndian.PutUint32(request[:4], workerMagic)
	binary.LittleEndian.PutUint32(request[4:8], c.sequence)
	binary.LittleEndian.PutUint16(request[8:10], op)
	request[10], request[11] = plane, 1
	binary.LittleEndian.PutUint16(request[12:14], selector)
	binary.LittleEndian.PutUint16(request[14:16], value)
	binary.LittleEndian.PutUint32(request[16:20], uint32(len(dst)))
	binary.LittleEndian.PutUint32(request[20:24], epoch)
	_ = c.conn.SetDeadline(time.Now().Add(3 * time.Second))
	n, err := c.conn.Write(request[:])
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
	if n-workerHeader > len(dst) {
		c.failed = fmt.Errorf("bus: oversized worker payload")
		c.conn.Close()
		return 0, c.failed
	}
	return copy(dst, c.response[workerHeader:n]), nil
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
