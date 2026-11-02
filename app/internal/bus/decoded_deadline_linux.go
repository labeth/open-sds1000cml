// ENGMODEL-OWNER-UNIT: FU-APP-BUS

package bus

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"syscall"
	"unsafe"
)

// DecodedDeadline belongs to the acquisition goroutine and its locked thread.
// The kernel's existing real-time throttling remains unchanged.
type DecodedDeadline struct {
	policy   uintptr
	priority int32
	procs    int
	// raised are the process's other threads RaiseProcess put at the same
	// real-time priority (restored on Close).
	raised []int
}

// StartDecodedDeadline reserves prompt FIFO service on the single-core ARM
// instrument. Two execution slots let HTTP run while this locked thread sleeps.
// Non-ARM hosts retain their normal scheduling for simulation and tests.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-001
func (d *Dev) StartDecodedDeadline() (*DecodedDeadline, error) {
	if d.remote != nil {
		return nil, nil
	} // the isolated owner handles its own scheduling
	if runtime.GOARCH != "arm" {
		return nil, nil
	}
	runtime.LockOSThread()
	s := &DecodedDeadline{}
	policy, _, err := syscall.RawSyscall(syscall.SYS_SCHED_GETSCHEDULER, 0, 0, 0)
	if err != 0 {
		runtime.UnlockOSThread()
		return nil, err
	}
	s.policy = policy
	_, _, err = syscall.RawSyscall(syscall.SYS_SCHED_GETPARAM, 0, uintptr(unsafe.Pointer(&s.priority)), 0) //nolint:gosec // G103: the system call takes its argument by address
	if err != 0 {
		runtime.UnlockOSThread()
		return nil, err
	}
	priority := int32(1)
	// Reset scheduling in any child thread the runtime creates here.
	const fifoResetOnFork = 1 | 0x40000000
	_, _, err = syscall.RawSyscall(syscall.SYS_SCHED_SETSCHEDULER, 0, fifoResetOnFork, uintptr(unsafe.Pointer(&priority))) //nolint:gosec // G103: the system call takes its argument by address
	if err != 0 {
		runtime.UnlockOSThread()
		return nil, fmt.Errorf("decoded deadline scheduling: %w", err)
	}
	if n := runtime.GOMAXPROCS(0); n < 2 {
		s.procs = n
		runtime.GOMAXPROCS(2)
	}
	return s, nil
}

// Pause gives display, controls and network a mandatory scheduling window after
// every service cycle. At 500 us, the USB fixture adds about 42 FIFO events.
// Sleep directly on this thread; a Go timer would depend on other goroutines.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-001
func (s *DecodedDeadline) Pause() error {
	ts := syscall.NsecToTimespec(500000)
	for {
		var remaining syscall.Timespec
		_, _, err := syscall.RawSyscall(syscall.SYS_NANOSLEEP, uintptr(unsafe.Pointer(&ts)), uintptr(unsafe.Pointer(&remaining)), 0) //nolint:gosec // G103: the system call takes its argument by address
		if err == 0 {
			return nil
		}
		if err != syscall.EINTR {
			return err
		}
		ts = remaining
	}
}

// RaiseProcess puts every other thread of this process at the deadline's
// real-time priority: Go preempts a goroutine that runs ten milliseconds
// without a scheduling point, and a locked thread gets its goroutine back
// only through another of the runtime's threads. At normal priority that
// thread waited behind the app for 17 to 32 ms, and the stream drum's
// banks went unread meanwhile. Only the acquisition worker, which allocates
// nothing while streaming, calls it.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-001
func (s *DecodedDeadline) RaiseProcess() {
	if s == nil {
		return
	}
	tasks, err := os.ReadDir("/proc/self/task")
	if err != nil {
		return
	}
	self := syscall.Gettid()
	priority := int32(1)
	const fifoResetOnFork = 1 | 0x40000000
	for _, t := range tasks {
		tid, err := strconv.Atoi(t.Name())
		if err != nil || tid == self {
			continue
		}
		if _, _, e := syscall.RawSyscall(syscall.SYS_SCHED_SETSCHEDULER, uintptr(tid), fifoResetOnFork, uintptr(unsafe.Pointer(&priority))); e == 0 { //nolint:gosec // G103: the system call takes its argument by address
			s.raised = append(s.raised, tid)
		}
	}
}

// Close restores the original policy before releasing this thread to Go. A
// restoration failure leaves it locked so it cannot run an unrelated goroutine.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-001
func (s *DecodedDeadline) Close() error {
	for _, tid := range s.raised {
		_, _, _ = syscall.RawSyscall(syscall.SYS_SCHED_SETSCHEDULER, uintptr(tid), s.policy, uintptr(unsafe.Pointer(&s.priority))) //nolint:gosec // G103: the system call takes its argument by address
	}
	s.raised = nil
	_, _, err := syscall.RawSyscall(syscall.SYS_SCHED_SETSCHEDULER, 0, s.policy, uintptr(unsafe.Pointer(&s.priority))) //nolint:gosec // G103: the system call takes its argument by address
	if err != 0 {
		return fmt.Errorf("restore decoded scheduling: %w", err)
	}
	runtime.UnlockOSThread()
	if s.procs != 0 {
		runtime.GOMAXPROCS(s.procs)
	}
	return nil
}
