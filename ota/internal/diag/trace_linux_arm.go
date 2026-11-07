// ENGMODEL-OWNER-UNIT: FU-OTA-DIAG

package diag

import (
	"bufio"
	"compress/gzip"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	core "github.com/labeth/open-dso-core/supervisor/diag"
)

// ARM EABI syscall numbers the trace records.
const (
	sysRead   = 3
	sysWrite  = 4
	sysOpen   = 5
	sysClose  = 6
	sysIoctl  = 54
	sysMmap2  = 192
	sysOpenat = 322
)

// gpmcRead and gpmcWrite are the factory GPMC driver's ioctl requests on its
// 6-byte record (plane, 0, selector, value; little-endian).
const (
	gpmcRead  = 0x80026700
	gpmcWrite = 0x40026701
)

// tracer follows every thread of one process through its syscalls.
type tracer struct {
	pid    int
	out    *bufio.Writer
	fdPath map[uint32]string
	entry  map[int]syscall.PtraceRegs // a thread's registers at its syscall's entry
	tids   map[int]bool
	lines  int
	// lastWrite is when the last GPMC register write was seen
	lastWrite time.Time
	counts    map[uint32]int
}

// Trace attaches to every thread of pid and writes one line per recorded
// syscall to o.Out (gzip'd): the time on CLOCK_MONOTONIC in microseconds, the
// thread, the call, its device and its decoded arguments. It returns after
// o.For, or once a GPMC register write was seen and o.QuietAfterWrite passed
// without another; its caller exits right after, and the kernel detaches and
// resumes every thread with it, so no thread is left stopped.
// TRLC-LINKS: REQ-SDS-285
func Trace(pid int, o TraceOpts) error {
	d, out, maxLines := o.For, o.Out, o.MaxLines
	runtime.LockOSThread()   // ptrace requests come from the attaching thread
	f, err := os.Create(out) //nolint:gosec // G304: the trace file on the stick
	if err != nil {
		return err
	}
	gz := gzip.NewWriter(f)
	t := &tracer{
		pid: pid, out: bufio.NewWriterSize(gz, 1<<20), fdPath: map[uint32]string{},
		entry: map[int]syscall.PtraceRegs{}, tids: map[int]bool{}, counts: map[uint32]int{},
	}
	defer func() {
		_, _ = fmt.Fprintf(t.out, "# end: %d lines, %d threads, calls by number %v\n", t.lines, len(t.tids), t.counts)
		_ = t.out.Flush()
		_ = gz.Close()
		_ = f.Close()
	}()
	ents, err := os.ReadDir(fmt.Sprintf("/proc/%d/task", pid))
	if err != nil {
		return err
	}
	for _, e := range ents {
		tid, _ := strconv.Atoi(e.Name())
		if err := syscall.PtraceAttach(tid); err != nil {
			_, _ = fmt.Fprintf(t.out, "# attach %d: %v\n", tid, err)
			continue
		}
		var ws syscall.WaitStatus
		if _, err := syscall.Wait4(tid, &ws, syscall.WALL, nil); err != nil {
			continue
		}
		_ = syscall.PtraceSetOptions(tid, syscall.PTRACE_O_TRACESYSGOOD|syscall.PTRACE_O_TRACECLONE)
		t.tids[tid] = true
		_ = syscall.PtraceSyscall(tid, 0)
	}
	if len(t.tids) == 0 {
		return fmt.Errorf("trace: no thread of %d attached", pid)
	}
	_, _ = fmt.Fprintf(t.out, "# pid %d threads %d start %d\n", pid, len(t.tids), core.MonoMicros())
	if o.Ready != nil {
		_, _ = fmt.Fprintln(o.Ready, "ready")
	}
	end := time.Now().Add(d)
	for time.Now().Before(end) && t.lines < maxLines && len(t.tids) > 0 {
		if o.QuietAfterWrite > 0 && !t.lastWrite.IsZero() && time.Since(t.lastWrite) > o.QuietAfterWrite {
			break
		}
		select {
		case <-o.Stop:
			return nil
		default:
		}
		var ws syscall.WaitStatus
		tid, err := syscall.Wait4(-1, &ws, syscall.WALL, nil)
		if err != nil {
			return err
		}
		t.step(tid, ws)
	}
	return nil
}

// step handles one stop of thread tid and resumes it.
// TRLC-LINKS: REQ-SDS-285
func (t *tracer) step(tid int, ws syscall.WaitStatus) {
	if ws.Exited() || ws.Signaled() {
		delete(t.tids, tid)
		return
	}
	if !ws.Stopped() {
		return
	}
	sig := 0
	switch s := ws.StopSignal(); {
	case s == syscall.SIGTRAP|0x80:
		// ARM says which stop this is in ip (r12): 0 at a syscall's entry, 1
		// at its exit. A thread attached inside a syscall stops at its exit
		// first; counting stops would read every call after it backwards.
		// Two words a stop (ip and the call's number in r7); the full set only
		// for a call the trace records: every stop holds the factory app up.
		ip, err1 := peekReg(tid, 12)
		nr, err2 := peekReg(tid, 7)
		switch {
		case err1 != nil || err2 != nil:
		case ip == 0:
			t.counts[nr]++
			var regs syscall.PtraceRegs
			if recorded(nr) && argRegs(tid, nr, &regs) == nil {
				t.entry[tid] = regs
			}
		default:
			if in, ok := t.entry[tid]; ok {
				delete(t.entry, tid)
				if r0, err := peekReg(tid, 0); err == nil {
					var out syscall.PtraceRegs
					out.Uregs[0] = r0
					t.exit(tid, in, out)
				}
			}
		}
	case ws.TrapCause() == syscall.PTRACE_EVENT_CLONE:
		if n, err := syscall.PtraceGetEventMsg(tid); err == nil {
			t.tids[int(n)] = true
		}
	case s == syscall.SIGSTOP || s == syscall.SIGTRAP:
		// a new thread's first stop, or the attach's own
	default:
		sig = int(s)
	}
	_ = syscall.PtraceSyscall(tid, sig)
}

// exit records a finished syscall worth keeping: an open, a close, a mmap2 of
// a device, and reads, writes and ioctls on /dev nodes.
// TRLC-LINKS: REQ-SDS-285
func (t *tracer) exit(tid int, in, out syscall.PtraceRegs) {
	nr := in.Uregs[7]
	ret := int32(out.Uregs[0]) //nolint:gosec // G115: the syscall's signed return in r0
	a0, a1, a2 := in.Uregs[0], in.Uregs[1], in.Uregs[2]
	now := core.MonoMicros()
	switch nr {
	case sysOpen, sysOpenat:
		path, flags := t.cstring(tid, a0), a1
		if nr == sysOpenat {
			path, flags = t.cstring(tid, a1), a2
		}
		if ret >= 0 {
			t.fdPath[uint32(ret)] = path
		}
		t.line("%d %d open %q flags=%#x ret=%d", now, tid, path, flags, ret)
	case sysClose:
		if p, ok := t.fdPath[a0]; ok && strings.HasPrefix(p, "/dev/") {
			t.line("%d %d close %d %s ret=%d", now, tid, a0, p, ret)
		}
		delete(t.fdPath, a0)
	case sysMmap2:
		t.line("%d %d mmap2 len=%#x prot=%#x flags=%#x fd=%d %s pgoff=%#x ret=%#x", now, tid, a1, a2, in.Uregs[3],
			int32(in.Uregs[4]), t.path(in.Uregs[4]), in.Uregs[5], out.Uregs[0]) //nolint:gosec // G115: the fd argument
	case sysIoctl:
		p := t.path(a0)
		if !strings.HasPrefix(p, "/dev/") {
			return
		}
		if p == "/dev/Gpmc" && (a1 == gpmcRead || a1 == gpmcWrite) {
			b := make([]byte, 6)
			if _, err := syscall.PtracePeekData(tid, uintptr(a2), b); err == nil {
				op := map[uint32]string{gpmcRead: "R", gpmcWrite: "W"}[a1]
				if a1 == gpmcWrite {
					t.lastWrite = time.Now()
				}
				t.line("%d %d gpmc %s %d %#04x %#04x ret=%d", now, tid, op, b[0],
					binary.LittleEndian.Uint16(b[2:]), binary.LittleEndian.Uint16(b[4:]), ret)
				return
			}
		}
		t.line("%d %d ioctl %s req=%#x arg=%s ret=%d", now, tid, p, a1, t.bytes(tid, a2, 32), ret)
		// SPI_IOC_MESSAGE(n): each spi_ioc_transfer names its transmit
		// buffer by address (tx_buf at 0, len at 16), so the bytes the
		// factory app sent (relay words, gain codes) are read from it
		if strings.HasPrefix(p, "/dev/spidev") && a1&0xc000ffff == 0x40006b00 {
			n := (a1 >> 16 & 0x3fff) / 32
			for i := range min(n, 8) {
				tr := make([]byte, 32)
				if _, err := syscall.PtracePeekData(tid, uintptr(a2+32*i), tr); err != nil {
					break
				}
				l := min(binary.LittleEndian.Uint32(tr[16:]), 64)
				t.line("%d %d spi %s tx=%s", now, tid, p, t.bytes(tid, binary.LittleEndian.Uint32(tr[0:]), l))
			}
		}
	case sysRead, sysWrite:
		p := t.path(a0)
		if !strings.HasPrefix(p, "/dev/spidev") && !strings.HasPrefix(p, "/dev/i2c") && !strings.HasPrefix(p, "/dev/tty") {
			return
		}
		n := uint32(max(ret, 0))
		t.line("%d %d %s %s len=%d ret=%d data=%s", now, tid, map[uint32]string{sysRead: "read", sysWrite: "write"}[nr],
			p, a2, ret, t.bytes(tid, a1, min(n, 64)))
	}
}

// argRegs reads the argument registers syscall nr needs at its entry, word
// by word (PTRACE_PEEKUSER; PTRACE_GETREGS fails with EIO on the scope's 3.2
// kernel): r0..r2 for most calls, r0..r5 for mmap2. Every word is a ptrace
// request while the factory app waits, and it polls its registers thousands
// of times a second.
// TRLC-LINKS: REQ-SDS-285
func argRegs(tid int, nr uint32, r *syscall.PtraceRegs) error {
	n := 3
	if nr == sysMmap2 {
		n = 6
	}
	for i := range n {
		v, err := peekReg(tid, i)
		if err != nil {
			return err
		}
		r.Uregs[i] = v
	}
	r.Uregs[7] = nr
	return nil
}

// peekReg reads register i of thread tid (PTRACE_PEEKUSER).
// TRLC-LINKS: REQ-SDS-285
func peekReg(tid, i int) (uint32, error) {
	var b [4]byte
	if _, err := unix.PtracePeekUser(tid, uintptr(4*i), b[:]); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(b[:]), nil
}

// recorded reports whether the trace records syscall nr.
// TRLC-LINKS: REQ-SDS-285
func recorded(nr uint32) bool {
	switch nr {
	case sysRead, sysWrite, sysOpen, sysOpenat, sysClose, sysIoctl, sysMmap2:
		return true
	}
	return false
}

// path is the target of the traced process's descriptor fd, cached.
// TRLC-LINKS: REQ-SDS-285
func (t *tracer) path(fd uint32) string {
	if p, ok := t.fdPath[fd]; ok {
		return p
	}
	p, _ := os.Readlink(fmt.Sprintf("/proc/%d/fd/%d", t.pid, fd))
	t.fdPath[fd] = p
	return p
}

// bytes is n bytes of the tracee's memory at addr, in hex.
// TRLC-LINKS: REQ-SDS-285
func (t *tracer) bytes(tid int, addr, n uint32) string {
	if addr == 0 || n == 0 {
		return "-"
	}
	b := make([]byte, n)
	if _, err := syscall.PtracePeekData(tid, uintptr(addr), b); err != nil {
		return "?"
	}
	return hex.EncodeToString(b)
}

// cstring is the NUL-terminated string at addr in the tracee, up to 256 bytes.
// TRLC-LINKS: REQ-SDS-285
func (t *tracer) cstring(tid int, addr uint32) string {
	b := make([]byte, 256)
	n, _ := syscall.PtracePeekData(tid, uintptr(addr), b)
	b = b[:n]
	if i := strings.IndexByte(string(b), 0); i >= 0 {
		b = b[:i]
	}
	return string(b)
}

// line writes one trace record.
// TRLC-LINKS: REQ-SDS-285
func (t *tracer) line(format string, args ...any) {
	_, _ = fmt.Fprintf(t.out, format+"\n", args...)
	t.lines++
}
