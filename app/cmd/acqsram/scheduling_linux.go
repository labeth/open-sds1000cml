// ENGMODEL-OWNER-UNIT: FU-APP-ACQSRAM
package main

import (
	"fmt"
	"runtime"
	"syscall"
	"unsafe"
)

// Diagnostic only: keep scheduler changes on one locked OS thread and restore
// its original policy before letting the Go runtime reuse it. Kernel RT
// throttling is deliberately left unchanged.
// TRLC-LINKS: REQ-SDS-170
func realtimeDrain() (func() error, error) {
	runtime.LockOSThread()
	policy, _, err := syscall.RawSyscall(syscall.SYS_SCHED_GETSCHEDULER, 0, 0, 0)
	if err != 0 {
		runtime.UnlockOSThread()
		return nil, err
	}
	var old int32
	_, _, err = syscall.RawSyscall(syscall.SYS_SCHED_GETPARAM, 0, uintptr(unsafe.Pointer(&old)), 0)
	if err != 0 {
		runtime.UnlockOSThread()
		return nil, err
	}
	priority := int32(1)
	_, _, err = syscall.RawSyscall(syscall.SYS_SCHED_SETSCHEDULER, 0, 1, uintptr(unsafe.Pointer(&priority)))
	if err != 0 {
		runtime.UnlockOSThread()
		return nil, fmt.Errorf("SCHED_FIFO: %w", err)
	}
	return func() error {
		_, _, e := syscall.RawSyscall(syscall.SYS_SCHED_SETSCHEDULER, 0, policy, uintptr(unsafe.Pointer(&old)))
		if e != 0 {
			return fmt.Errorf("restore scheduling: %w", e)
		}
		runtime.UnlockOSThread()
		return nil
	}, nil
}

// Sleep directly on the locked diagnostic thread rather than waiting for a Go
// timer goroutine to be scheduled on this single-core device.
// TRLC-LINKS: REQ-SDS-170
func sleepDrain(ns int64) error {
	ts := syscall.NsecToTimespec(ns)
	for {
		var rem syscall.Timespec
		_, _, e := syscall.RawSyscall(syscall.SYS_NANOSLEEP, uintptr(unsafe.Pointer(&ts)), uintptr(unsafe.Pointer(&rem)), 0)
		if e == 0 {
			return nil
		}
		if e != syscall.EINTR {
			return e
		}
		ts = rem
	}
}
