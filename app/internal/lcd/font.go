// ENGMODEL-OWNER-UNIT: FU-APP-LCD

package lcd

import "github.com/labeth/open-dso-core/design"

// glyphs is the shared display font (design.Glyphs).
var glyphs = design.Glyphs

// TextWidth is len(runes) × 6 × scale.
// TRLC-LINKS: REQ-SDS-021
func TextWidth(s string, scale int) int {
	n := 0
	for range s {
		n++
	}
	return n * 6 * scale
}

// DrawText renders each set bit as a scale×scale block; advance 6·scale.
// Bit 7 is a descender row: g, j, p, q and y drop below the baseline, so g
// does not read as 9 ("Sp9m", "phase de9").
// TRLC-LINKS: REQ-SDS-021
func DrawText(sf Surface, x, y int, s string, c uint16, scale int) {
	m, mem := sf.(*MemSurface)
	lo, hi := byte(c&0xff), byte(c>>8)
	for _, r := range s {
		var g [5]byte
		ok := false
		if r >= 0 && r < 128 {
			g, ok = asciiGlyphs[r], asciiHave[r]
		} else {
			g, ok = glyphs[r]
		}
		switch {
		case !ok:
		case mem && scale == 1 && x >= 0 && y >= 0 && x+5 <= W && y+8 <= H:
			// Most of the HUD: one pixel a set bit, a column at a time.
			glyphPx(m.px(), y*W+x, &g, c)
		case mem && x >= 0 && y >= 0 && x+5*scale <= W && y+8*scale <= H:
			// The glyph's box is on the screen: its pixels written
			// directly, as SetPixel would write them.
			glyphPix(m.Pix, x, y, &g, scale, lo, hi)
		default:
			glyphAt(sf, x, y, &g, scale, c)
		}
		x += 6 * scale
	}
}

// glyphPx writes a glyph at scale 1 into the page's words from offset o (its
// top-left pixel): one pixel a set bit, a column at a time.
// TRLC-LINKS: REQ-SDS-021
func glyphPx(px []uint16, o int, g *[5]byte, c uint16) {
	for col := range 5 {
		p := o + col
		for bits := g[col]; bits != 0; bits >>= 1 {
			if bits&1 != 0 {
				px[p] = c
			}
			p += W
		}
	}
}

// glyphPix writes a scaled glyph whose box is on the page straight into its
// bytes (lo, hi: the colour's two), as SetPixel would write them.
// TRLC-LINKS: REQ-SDS-021
func glyphPix(pix []byte, x, y int, g *[5]byte, scale int, lo, hi byte) {
	for col := range 5 {
		bits := g[col]
		for row := range 8 {
			if bits&(1<<uint(row)) == 0 {
				continue
			}
			for dy := range scale {
				o := ((y+row*scale+dy)*W + x + col*scale) * 2
				for dx := range scale {
					pix[o+2*dx], pix[o+2*dx+1] = lo, hi
				}
			}
		}
	}
}

// glyphAt draws a glyph pixel by pixel through the surface, clipped by it.
// TRLC-LINKS: REQ-SDS-021
func glyphAt(sf Surface, x, y int, g *[5]byte, scale int, c uint16) {
	for col := range 5 {
		bits := g[col]
		for row := range 8 { // row 7: descenders (g j p q y)
			if bits&(1<<uint(row)) == 0 {
				continue
			}
			for dy := range scale {
				for dx := range scale {
					sf.SetPixel(x+col*scale+dx, y+row*scale+dy, c)
				}
			}
		}
	}
}

// asciiGlyphs and asciiHave are the glyph table's ASCII part as arrays: a
// lookup a character without the map.
var asciiGlyphs, asciiHave = func() (g [128][5]byte, have [128]bool) {
	for r, v := range glyphs {
		if r >= 0 && r < 128 {
			g[r], have[r] = v, true
		}
	}
	return g, have
}()

// DrawTextRight right-aligns: draws ending at xr.
// TRLC-LINKS: REQ-SDS-021
func DrawTextRight(sf Surface, xr, y int, s string, c uint16, scale int) {
	DrawText(sf, xr-TextWidth(s, scale), y, s, c, scale)
}
