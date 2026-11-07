// Package lcd is the SDS1000CML+'s display device (spec 07): the 800×480
// RGB565-LE panel behind /dev/fb0, double-buffered by y-pan, its bring-up
// and backlight, and the SCDP hardcopy. The core's render package draws the
// pictures; this package only presents them. It never touches the GPMC bus.
// ENGMODEL-OWNER-UNIT: FU-APP-LCD
package lcd

import (
	"encoding/binary"
	"fmt"
	"os"
	"syscall"
	"unsafe"

	"github.com/labeth/open-dso-core/render"
)

const (
	// W is the panel's width in pixels.
	W = 800
	// H is the panel's height in pixels.
	H = 480
	// pageBytes is one RGB565 page of the panel.
	pageBytes = W * H * 2
)

// FB is the real framebuffer: a FRESH open of /dev/fb0 (the opposite of the
// GPMC/fpga_key fds, which must be inherited — the fb is a plain char device
// completely off the GPMC bus).
// TRLC-LINKS: REQ-SDS-021
type FB struct {
	fd    int
	mem   []byte
	pages int
	cur   int
	panOK bool
	vinfo [160]byte // cached fb_var_screeninfo (32-bit ARM layout)
}

const (
	fbioGetVScreenInfo = 0x4600
	fbioPutVScreenInfo = 0x4601
	fbioPanDisplay     = 0x4606
	fbActivateForce    = 0x80

	offYresVirtual = 12
	offYoffset     = 20
	offActivate    = 84
)

// TRLC-LINKS: REQ-SDS-021
func fbIoctl(fd int, req uintptr, p unsafe.Pointer) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), req, uintptr(p))
	if errno != 0 {
		return errno
	}
	return nil
}

// OpenFB opens and maps /dev/fb0. yresVirtual comes from
// /sys/class/graphics/fb0/virtual_size ("xres,yres"), defaulting to 960
// (two stacked pages).
// TRLC-LINKS: REQ-SDS-021
func OpenFB() (*FB, error) {
	fd, err := syscall.Open("/dev/fb0", syscall.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("lcd: open /dev/fb0: %w", err)
	}
	yv := 960
	if b, err := os.ReadFile("/sys/class/graphics/fb0/virtual_size"); err == nil {
		var x, y int
		if n, _ := fmt.Sscanf(string(b), "%d,%d", &x, &y); n == 2 && y >= H {
			yv = y
		}
	}
	pages := max(yv/H, 1)
	mem, err := syscall.Mmap(fd, 0, W*yv*2, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		_ = syscall.Close(fd) // the mmap failure is the error reported
		return nil, fmt.Errorf("lcd: mmap fb: %w", err)
	}
	fb := &FB{fd: fd, mem: mem, pages: pages, panOK: pages >= 2}
	if err := fbIoctl(fd, fbioGetVScreenInfo, unsafe.Pointer(&fb.vinfo[0])); err != nil { //nolint:gosec // the ioctl fills the fb_var_screeninfo bytes
		fb.panOK = false
	}
	return fb, nil
}

// Close releases a temporary framebuffer mapping. The owner must stop drawing
// before calling it; it does not close inherited acquisition descriptors.
// TRLC-LINKS: REQ-SDS-021
func (fb *FB) Close() error {
	var err error
	if fb.mem != nil {
		err = syscall.Munmap(fb.mem)
		fb.mem = nil
	}
	if fb.fd >= 0 {
		closeErr := syscall.Close(fb.fd)
		fb.fd = -1
		if err == nil {
			err = closeErr
		}
	}
	return err
}

// TRLC-LINKS: REQ-SDS-021
func (fb *FB) put32(off int, v uint32) {
	binary.LittleEndian.PutUint32(fb.vinfo[off:], v)
}

// pan flips scanout to a page: FBIOPAN_DISPLAY first, then the
// FBIOPUT_VSCREENINFO + FB_ACTIVATE_FORCE fallback (some drivers honour
// only one).
// TRLC-LINKS: REQ-SDS-021
func (fb *FB) pan(page int) error {
	fb.put32(offYoffset, uint32(page*H))                                                 //nolint:gosec // page < fb.pages (a handful): the row offset is small
	if err := fbIoctl(fb.fd, fbioPanDisplay, unsafe.Pointer(&fb.vinfo[0])); err == nil { //nolint:gosec // the ioctl reads the fb_var_screeninfo bytes
		return nil
	}
	fb.put32(offActivate, fbActivateForce)
	err := fbIoctl(fb.fd, fbioPutVScreenInfo, unsafe.Pointer(&fb.vinfo[0])) //nolint:gosec // the ioctl reads the fb_var_screeninfo bytes
	fb.put32(offActivate, 0)
	return err
}

// EncodeBMP wraps a surface as the SCDP hardcopy (spec 11 §5): Windows BMP,
// BI_BITFIELDS 16bpp, top-down, RGB565 masks, pixel data at offset 66 — a
// straight memcpy of the framebuffer bytes, no byte swap.
// TRLC-LINKS: REQ-SDS-021
func EncodeBMP(m *render.Image565) []byte {
	const hdrSize = 66
	total := hdrSize + pageBytes
	b := make([]byte, hdrSize, total)
	b[0], b[1] = 'B', 'M'
	le32 := func(off int, v uint32) {
		binary.LittleEndian.PutUint32(b[off:], v)
	}
	le32(2, uint32(total))
	le32(10, hdrSize)
	le32(14, 40) // BITMAPINFOHEADER
	le32(18, W)
	le32(22, 1<<32-H)    // negative height (two's complement) = top-down rows
	b[26], b[28] = 1, 16 // planes, bpp
	le32(30, 3)          // BI_BITFIELDS
	le32(34, uint32(pageBytes))
	le32(54, 0xF800) // red mask
	le32(58, 0x07E0) // green
	le32(62, 0x001F) // blue
	return append(b, m.Pix...)
}

// Present publishes a complete back buffer: copy into the HIDDEN page and
// flip (tear-free); if panning is unavailable, copy into every page.
// TRLC-LINKS: REQ-SDS-021
func (fb *FB) Present(back *render.Image565) {
	if fb.panOK {
		next := 1 - fb.cur
		copyPage(fb.mem[next*pageBytes:(next+1)*pageBytes], back.Pix)
		if fb.pan(next) == nil {
			fb.cur = next
			return
		}
		fb.panOK = false
	}
	for p := range fb.pages {
		copyPage(fb.mem[p*pageBytes:(p+1)*pageBytes], back.Pix)
	}
}

// PresentRows publishes rows y0..y1-1 of the back buffer into the page on
// show, in place: a small region (the loading indicator) without copying
// the screen or flipping. The next Present copies the whole buffer.
// TRLC-LINKS: REQ-SDS-021
func (fb *FB) PresentRows(back *render.Image565, y0, y1 int) {
	y0, y1 = max(0, y0), min(H, y1)
	if y1 <= y0 {
		return
	}
	a, b := y0*W*2, y1*W*2
	if fb.panOK {
		copy(fb.mem[fb.cur*pageBytes+a:fb.cur*pageBytes+b], back.Pix[a:b])
		return
	}
	for p := range fb.pages {
		copy(fb.mem[p*pageBytes+a:p*pageBytes+b], back.Pix[a:b])
	}
}
