// ENGMODEL-OWNER-UNIT: FU-OTA-DIAG

package diag

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"strconv"
	"strings"
)

// fbGeom is the framebuffer's geometry as sysfs reports it.
type fbGeom struct {
	W, H, BPP, Stride, PanY int
}

// readFBGeom reads fb0's visible size, depth, line length and pan.
// TRLC-LINKS: REQ-SDS-284
func readFBGeom(dir string) (fbGeom, error) {
	var g fbGeom
	pair := func(name string) (int, int) {
		f := strings.Split(strings.TrimSpace(readStr(dir+"/"+name)), ",")
		if len(f) != 2 {
			return 0, 0
		}
		a, _ := strconv.Atoi(f[0])
		b, _ := strconv.Atoi(f[1])
		return a, b
	}
	g.W, g.H = pair("virtual_size")
	_, g.PanY = pair("pan")
	g.BPP, _ = strconv.Atoi(strings.TrimSpace(readStr(dir + "/bits_per_pixel")))
	g.Stride, _ = strconv.Atoi(strings.TrimSpace(readStr(dir + "/stride")))
	if g.W <= 0 || g.H <= 0 || (g.BPP != 16 && g.BPP != 24 && g.BPP != 32) {
		return g, fmt.Errorf("framebuffer %dx%d at %d bpp: not read", g.W, g.H, g.BPP)
	}
	if g.Stride <= 0 {
		g.Stride = g.W * g.BPP / 8
	}
	mode := strings.TrimSpace(readStr(dir + "/mode")) // "U:800x480p-0": the visible part of a panned buffer
	if mode == "" {
		mode, _, _ = strings.Cut(strings.TrimSpace(readStr(dir+"/modes")), "\n")
	}
	if _, m, ok := strings.Cut(mode, ":"); ok {
		var w, h int
		if _, err := fmt.Sscanf(m, "%dx%d", &w, &h); err == nil && w > 0 && h > 0 && h <= g.H {
			g.W, g.H = min(w, g.W), h
		}
	}
	return g, nil
}

// fbImage converts one screen of raw framebuffer bytes to an image.
// TRLC-LINKS: REQ-SDS-284
func fbImage(raw []byte, g fbGeom) (image.Image, error) {
	if len(raw) < g.H*g.Stride {
		return nil, errors.New("framebuffer: short read")
	}
	im := image.NewRGBA(image.Rect(0, 0, g.W, g.H))
	for y := range g.H {
		row := raw[y*g.Stride:]
		for x := range g.W {
			var c color.RGBA
			switch g.BPP {
			case 16:
				v := uint16(row[2*x]) | uint16(row[2*x+1])<<8
				c = color.RGBA{R: uint8(v >> 11 << 3), G: uint8(v >> 5 << 2), B: uint8(v << 3), A: 255} //nolint:gosec // G115: RGB565 fields widened to 8 bits
			case 24:
				c = color.RGBA{R: row[3*x+2], G: row[3*x+1], B: row[3*x], A: 255}
			default:
				c = color.RGBA{R: row[4*x+2], G: row[4*x+1], B: row[4*x], A: 255}
			}
			im.SetRGBA(x, y, c)
		}
	}
	return im, nil
}

// collectScreen stores the factory UI's screen as a PNG, and the
// framebuffer's sysfs attributes with it.
// TRLC-LINKS: REQ-SDS-284
func collectScreen(a *Archive, name string) string {
	g, err := readFBGeom("/sys/class/graphics/fb0")
	if err != nil {
		a.Errorf("screen: %v", err)
		return err.Error()
	}
	f, err := os.Open("/dev/fb0")
	if err != nil {
		a.Errorf("screen: %v", err)
		return err.Error()
	}
	defer func() { _ = f.Close() }()
	raw := make([]byte, g.H*g.Stride)
	// a seek and full reads: the framebuffer driver can return a positioned read short
	if _, err := f.Seek(int64(g.PanY)*int64(g.Stride), io.SeekStart); err != nil {
		a.Errorf("screen: %v", err)
		return err.Error()
	}
	if _, err := io.ReadFull(f, raw); err != nil {
		a.Errorf("screen: %v", err)
		return err.Error()
	}
	im, err := fbImage(raw, g)
	if err != nil {
		a.Errorf("screen: %v", err)
		return err.Error()
	}
	var b bytes.Buffer
	if err := png.Encode(&b, im); err != nil {
		a.Errorf("screen: %v", err)
		return err.Error()
	}
	a.Add("screen/"+name+".png", b.Bytes(), "/dev/fb0", fmt.Sprintf("%dx%d at %d bpp, pan %d", g.W, g.H, g.BPP, g.PanY))
	return fmt.Sprintf("%dx%d at %d bpp", g.W, g.H, g.BPP)
}
