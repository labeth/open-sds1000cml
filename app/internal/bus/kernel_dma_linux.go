// ENGMODEL-OWNER-UNIT: FU-APP-BUS
package bus

import (
	"fmt"
	"os"
	"runtime"
	"syscall"
	"unsafe"
)

// TRLC-LINKS: REQ-SDS-134
type kernelDMADrainer struct {
	file      *os.File
	bytes     []byte
	streaming bool
}

// EnableKernelDMA is experimental and exclusive with userspace EDMA. The
// module holds a reference to our inherited GPMC fd; it never opens GPMC.
// TRLC-LINKS: REQ-SDS-002, REQ-SDS-081, REQ-SDS-134
func (d *Dev) EnableKernelDMA() error {
	if unsafe.Sizeof(uintptr(0)) != 4 {
		return fmt.Errorf("bus: kernel DMA ABI requires 32-bit ARM scope")
	}
	if d.edma != nil || d.kernelDMA != nil {
		return fmt.Errorf("bus: DMA already enabled")
	}
	f, err := os.OpenFile("/dev/acq_dma", os.O_RDWR, 0)
	if err != nil {
		return err
	}
	fail := func(e error) error { f.Close(); return e }
	fd := uint32(d.fd)
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), 0x40045102, uintptr(unsafe.Pointer(&fd))); e != 0 {
		return fail(e)
	}
	// No FPGA read: module self-test copies coherent RAM through its IRQ path.
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), 0x5100, 0); e != 0 {
		return fail(e)
	}
	if err = programCS1CycleGap(cs1CycleGap); err != nil {
		return fail(err)
	}
	d.kernelDMA = &kernelDMADrainer{file: f, bytes: make([]byte, 16384)}
	return nil
}
// TRLC-LINKS: REQ-SDS-002, REQ-SDS-134
func (d *Dev) CloseKernelDMA() error {
	if d.kernelDMA == nil {
		return nil
	}
	err := d.kernelDMA.file.Close()
	d.kernelDMA = nil
	return err
}
// TRLC-LINKS: REQ-SDS-134
func (k *kernelDMADrainer) pop(sel uint16, dst []byte) error {
	if k.streaming {
		return fmt.Errorf("bus: kernel stream owns GPMC until CloseKernelDMA")
	}
	if sel != 25 || len(dst) == 0 || len(dst) > 16384 || len(dst)%2 != 0 {
		return fmt.Errorf("bus: invalid kernel pop selector/size")
	}
	a := [4]uint32{uint32(len(dst)), uint32(uintptr(unsafe.Pointer(&dst[0]))), 0, 0}
	_, _, e := syscall.Syscall(syscall.SYS_IOCTL, k.file.Fd(), 0x40105101, uintptr(unsafe.Pointer(&a)))
	runtime.KeepAlive(dst)
	if e != 0 {
		return fmt.Errorf("bus: kernel DMA pop failed (pointer may have advanced): %w", e)
	}
	return nil
}

// StartKernelStream transfers exclusive GPMC ownership to the kernel worker.
// No Read/RawWrite/Pop calls are allowed until EOF and CloseKernelDMA. This
// bounded experimental ABI uses source 0=ADC, 1=counter and a word target.
// TRLC-LINKS: REQ-SDS-134
func (d *Dev) StartKernelStream(log, source, target, seconds uint32) error {
	if d.kernelDMA == nil {
		return fmt.Errorf("bus: kernel DMA disabled")
	}
	if d.kernelDMA.streaming {
		return fmt.Errorf("bus: kernel stream already started")
	}
	if log < 8 || log > 20 || source > 1 || target == 0 || seconds == 0 || seconds > 30 {
		return fmt.Errorf("bus: invalid kernel stream configuration")
	}
	a := [4]uint32{log, source, target, seconds}
	_, _, e := syscall.Syscall(syscall.SYS_IOCTL, d.kernelDMA.file.Fd(), 0x40105103, uintptr(unsafe.Pointer(&a)))
	if e != 0 {
		return e
	}
	d.kernelDMA.streaming = true
	return nil
}

// ReadKernelStream returns one complete LE header (first word u64, word count
// u32, reserved u32) followed by sample bytes, or EOF after all banks drain.
// TRLC-LINKS: REQ-SDS-134
func (d *Dev) ReadKernelStream(dst []byte) (int, error) {
	if d.kernelDMA == nil {
		return 0, fmt.Errorf("bus: kernel DMA disabled")
	}
	if !d.kernelDMA.streaming {
		return 0, fmt.Errorf("bus: kernel stream not started")
	}
	if len(dst) < 16400 {
		return 0, fmt.Errorf("bus: kernel stream buffer too small")
	}
	return d.kernelDMA.file.Read(dst)
}

// TRLC-LINKS: REQ-SDS-134
func (d *Dev) KernelStreamStats() ([8]uint32, error) {
	var stats [8]uint32
	if d.kernelDMA == nil {
		return stats, fmt.Errorf("bus: kernel DMA disabled")
	}
	_, _, e := syscall.Syscall(syscall.SYS_IOCTL, d.kernelDMA.file.Fd(), 0x80205104, uintptr(unsafe.Pointer(&stats)))
	if e != 0 {
		return stats, e
	}
	return stats, nil
}
