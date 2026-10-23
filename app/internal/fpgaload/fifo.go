// ENGMODEL-OWNER-UNIT: FU-APP-FPGALOAD
package fpgaload

import (
	"runtime"
	"syscall"
	"unsafe"
)

// fifoShift runs the bit shift at real-time priority on its own thread (the
// scope's ARM has one core): at normal priority the display, the web server
// and the frame fan-out took turns with it and an image shifted at half the
// port's rate (0.6 against 1.2 Mbit/s). The kernel's real-time throttling
// still gives the rest a slice each second. Elsewhere the shift runs as is.
// TRLC-LINKS: REQ-SDS-005
func fifoShift(shift func() error) error {
	if runtime.GOARCH != "arm" {
		return shift()
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	policy, _, e1 := syscall.RawSyscall(syscall.SYS_SCHED_GETSCHEDULER, 0, 0, 0)
	var prio int32
	_, _, e2 := syscall.RawSyscall(syscall.SYS_SCHED_GETPARAM, 0, uintptr(unsafe.Pointer(&prio)), 0)
	if e1 != 0 || e2 != 0 {
		return shift()
	}
	fifo := int32(1)
	const fifoResetOnFork = 1 | 0x40000000
	if _, _, e := syscall.RawSyscall(syscall.SYS_SCHED_SETSCHEDULER, 0, fifoResetOnFork, uintptr(unsafe.Pointer(&fifo))); e != 0 {
		return shift()
	}
	err := shift()
	syscall.RawSyscall(syscall.SYS_SCHED_SETSCHEDULER, 0, policy, uintptr(unsafe.Pointer(&prio)))
	return err
}
