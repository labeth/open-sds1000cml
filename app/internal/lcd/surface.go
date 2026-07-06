// Package lcd renders the scope display to the device panel (spec 07):
// 800×480 RGB565-LE via /dev/fb0, double-buffered by y-pan. The renderer is
// a pure consumer — it never touches the GPMC bus; its only cross-goroutine
// touch point is the frame fan-out lock.
package lcd

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"syscall"
	"unsafe"
)

const (
	W         = 800
	H         = 480
	pageBytes = W * H * 2
)

// RGB565 packing: pixel = (R>>3)<<11 | (G>>2)<<5 | (B>>3).
func rgb(r, g, b uint8) uint16 {
	return uint16(r>>3)<<11 | uint16(g>>2)<<5 | uint16(b>>3)
}

// Surface is the only drawing contract the renderer sees (spec 07 §1.3):
// the same renderer must run byte-identically against /dev/fb0 and an
// in-memory test surface.
type Surface interface {
	SetPixel(x, y int, c uint16) // out-of-range ignored
	Fill(c uint16)
	At(x, y int) uint16 // out-of-range returns 0
}

// MemSurface is the back buffer (exactly one page) and the test surface.
type MemSurface struct {
	Pix []byte // RGB565 little-endian, W*H*2 bytes
}

func NewMemSurface() *MemSurface { return &MemSurface{Pix: make([]byte, pageBytes)} }

func (m *MemSurface) SetPixel(x, y int, c uint16) {
	if x < 0 || x >= W || y < 0 || y >= H {
		return
	}
	o := (y*W + x) * 2
	m.Pix[o] = byte(c)
	m.Pix[o+1] = byte(c >> 8)
}

func (m *MemSurface) Fill(c uint16) {
	lo, hi := byte(c), byte(c>>8)
	row := m.Pix[:W*2]
	for x := 0; x < W; x++ {
		row[x*2], row[x*2+1] = lo, hi
	}
	for y := 1; y < H; y++ {
		copy(m.Pix[y*W*2:(y+1)*W*2], row)
	}
}

func (m *MemSurface) At(x, y int) uint16 {
	if x < 0 || x >= W || y < 0 || y >= H {
		return 0
	}
	o := (y*W + x) * 2
	return uint16(m.Pix[o]) | uint16(m.Pix[o+1])<<8
}

// FadeToBlack dims every RGB565 pixel toward black by 1/4 (the persistence decay
// step). Pixels already black are skipped. Traces drawn onto this layer at full
// brightness thus glow and fade over ~8 frames.
func (m *MemSurface) FadeToBlack() {
	p := m.Pix
	for i := 0; i+1 < len(p); i += 2 {
		c := uint16(p[i]) | uint16(p[i+1])<<8
		if c == 0 {
			continue
		}
		r := ((c >> 11) & 0x1f) * 3 / 4
		g := ((c >> 5) & 0x3f) * 3 / 4
		b := (c & 0x1f) * 3 / 4
		nc := r<<11 | g<<5 | b
		p[i], p[i+1] = byte(nc), byte(nc>>8)
	}
}

// BlitBright copies the non-black (bright enough) pixels of src onto m — the
// persistence trace layer composited over the fresh graticule.
func (m *MemSurface) BlitBright(src *MemSurface) {
	d, s := m.Pix, src.Pix
	n := len(s)
	if len(d) < n {
		n = len(d)
	}
	for i := 0; i+1 < n; i += 2 {
		c := uint16(s[i]) | uint16(s[i+1])<<8
		if c == 0 {
			continue
		}
		if (c>>11)&0x1f+(c>>6)&0x1f+c&0x1f < 3 { // too dim → let the graticule show
			continue
		}
		d[i], d[i+1] = s[i], s[i+1]
	}
}

// EncodePNG renders the surface (RGB565) to a PNG — the device-screen view for
// the web /api/screen.png endpoint.
func EncodePNG(m *MemSurface) []byte {
	img := image.NewRGBA(image.Rect(0, 0, W, H))
	for y := 0; y < H; y++ {
		for x := 0; x < W; x++ {
			c := m.At(x, y)
			r := uint8((c>>11)&0x1f) << 3
			g := uint8((c>>5)&0x3f) << 2
			b := uint8(c&0x1f) << 3
			img.SetRGBA(x, y, color.RGBA{r, g, b, 255})
		}
	}
	var buf bytes.Buffer
	png.Encode(&buf, img)
	return buf.Bytes()
}

// FB is the real framebuffer: a FRESH open of /dev/fb0 (the opposite of the
// GPMC/fpga_key fds, which must be inherited — the fb is a plain char device
// completely off the GPMC bus).
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
	pages := yv / H
	if pages < 1 {
		pages = 1
	}
	mem, err := syscall.Mmap(fd, 0, W*yv*2, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		syscall.Close(fd)
		return nil, fmt.Errorf("lcd: mmap fb: %w", err)
	}
	fb := &FB{fd: fd, mem: mem, pages: pages, panOK: pages >= 2}
	if err := fbIoctl(fd, fbioGetVScreenInfo, unsafe.Pointer(&fb.vinfo[0])); err != nil {
		fb.panOK = false
	}
	return fb, nil
}

func (fb *FB) put32(off int, v uint32) {
	fb.vinfo[off] = byte(v)
	fb.vinfo[off+1] = byte(v >> 8)
	fb.vinfo[off+2] = byte(v >> 16)
	fb.vinfo[off+3] = byte(v >> 24)
}

// pan flips scanout to a page: FBIOPAN_DISPLAY first, then the
// FBIOPUT_VSCREENINFO + FB_ACTIVATE_FORCE fallback (some drivers honour
// only one).
func (fb *FB) pan(page int) error {
	fb.put32(offYoffset, uint32(page*H))
	if err := fbIoctl(fb.fd, fbioPanDisplay, unsafe.Pointer(&fb.vinfo[0])); err == nil {
		return nil
	}
	fb.put32(offActivate, fbActivateForce)
	err := fbIoctl(fb.fd, fbioPutVScreenInfo, unsafe.Pointer(&fb.vinfo[0]))
	fb.put32(offActivate, 0)
	return err
}

// EncodeBMP wraps a surface as the SCDP hardcopy (spec 11 §5): Windows BMP,
// BI_BITFIELDS 16bpp, top-down, RGB565 masks, pixel data at offset 66 — a
// straight memcpy of the framebuffer bytes, no byte swap.
func EncodeBMP(m *MemSurface) []byte {
	const hdrSize = 66
	total := hdrSize + pageBytes
	b := make([]byte, hdrSize, total)
	b[0], b[1] = 'B', 'M'
	le32 := func(off int, v uint32) {
		b[off] = byte(v)
		b[off+1] = byte(v >> 8)
		b[off+2] = byte(v >> 16)
		b[off+3] = byte(v >> 24)
	}
	le32(2, uint32(total))
	le32(10, hdrSize)
	le32(14, 40) // BITMAPINFOHEADER
	le32(18, W)
	negH := int32(-H) // negative height = top-down rows
	le32(22, uint32(negH))
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
func (fb *FB) Present(back *MemSurface) {
	if fb.panOK {
		next := 1 - fb.cur
		copy(fb.mem[next*pageBytes:(next+1)*pageBytes], back.Pix)
		if fb.pan(next) == nil {
			fb.cur = next
			return
		}
		fb.panOK = false
	}
	for p := 0; p < fb.pages; p++ {
		copy(fb.mem[p*pageBytes:(p+1)*pageBytes], back.Pix)
	}
}
