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
)

// ARM EABI syscall numbers the trace records.
const (
	sysRead  = 3
	sysWrite = 4
	sysOpen  = 5
	sysClose = 6
	sysIoctl = 54
	sysMmap2 = 192
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
	inSys  map[int]bool
	tids   map[int]bool
	lines  int
	counts map[uint32]int
}

// Trace attaches to every thread of pid for d and writes one line per
// recorded syscall to out (gzip'd): the time on CLOCK_MONOTONIC in
// microseconds, the thread, the call, its device and its decoded arguments.
// It returns when d has passed; its caller exits right after, and the kernel
// detaches and resumes every thread with it, so no thread is left stopped.
// TRLC-LINKS: REQ-SDS-285
func Trace(pid int, d time.Duration, out string, maxLines int) error {
	runtime.LockOSThread()   // ptrace requests come from the attaching thread
	f, err := os.Create(out) //nolint:gosec // G304: the trace file on the stick
	if err != nil {
		return err
	}
	gz := gzip.NewWriter(f)
	t := &tracer{
		pid: pid, out: bufio.NewWriterSize(gz, 1<<20), fdPath: map[uint32]string{},
		entry: map[int]syscall.PtraceRegs{}, inSys: map[int]bool{}, tids: map[int]bool{}, counts: map[uint32]int{},
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
	_, _ = fmt.Fprintf(t.out, "# pid %d threads %d start %d\n", pid, len(t.tids), monoMicros())
	end := time.Now().Add(d)
	for time.Now().Before(end) && t.lines < maxLines && len(t.tids) > 0 {
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
		var regs syscall.PtraceRegs
		if syscall.PtraceGetRegs(tid, &regs) == nil {
			if t.inSys[tid] {
				t.exit(tid, t.entry[tid], regs)
			} else {
				t.entry[tid] = regs
			}
			t.inSys[tid] = !t.inSys[tid]
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
	t.counts[nr]++
	ret := int32(out.Uregs[0]) //nolint:gosec // G115: the syscall's signed return in r0
	a0, a1, a2 := in.Uregs[0], in.Uregs[1], in.Uregs[2]
	now := monoMicros()
	switch nr {
	case sysOpen:
		path := t.cstring(tid, a0)
		if ret >= 0 {
			t.fdPath[uint32(ret)] = path
		}
		t.line("%d %d open %q flags=%#x ret=%d", now, tid, path, a1, ret)
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
				t.line("%d %d gpmc %s %d %#04x %#04x ret=%d", now, tid, op, b[0],
					binary.LittleEndian.Uint16(b[2:]), binary.LittleEndian.Uint16(b[4:]), ret)
				return
			}
		}
		t.line("%d %d ioctl %s req=%#x arg=%s ret=%d", now, tid, p, a1, t.bytes(tid, a2, 32), ret)
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
