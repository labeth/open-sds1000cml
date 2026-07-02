package engine

import "math"

// Acquisition modes (spec 03 §7.4, spec 09): ERES boxcar, edge-aligned
// AVERAGE ring, PEAK (a stored no-op at real-time bands — the envelope/roll
// paths ARE peak-detect by construction), and the cross-frame uniformity
// telemetry. All pure CPU on the owner goroutine, zero bus access.

const (
	AcqNormal  = 0
	AcqAverage = 1
	AcqEres    = 2
	AcqPeak    = 3 // accepted/stored; behaves as NORMAL at real-time bands
)

// EresLenForBits maps enhancement bits to a boxcar length: L = round(4^b),
// clamped [1,64], forced odd (even L → L−1 so 64 → 63 stays in range).
func EresLenForBits(bits float64) int {
	l := int(math.Round(math.Pow(4, bits)))
	return clampEresLen(l)
}

func clampEresLen(l int) int {
	if l < 1 {
		l = 1
	}
	if l > 64 {
		l = 64
	}
	if l > 1 && l%2 == 0 {
		l--
	}
	return l
}

// eresBoxcar applies a symmetric odd-length moving average IN PLACE to the
// whole record — BEFORE edge detection, so the trigger anchor and the
// display see the same enhanced samples. At the record ends the kernel
// shrinks to the available samples: no wrap-around, no fabricated tail.
func eresBoxcar(sig []uint8, l int, scratch []uint16) {
	n := len(sig)
	if l <= 1 || n == 0 {
		return
	}
	half := l / 2
	// Prefix sums into scratch (uint32 could overflow uint16 at n>257·255;
	// use a rolling window sum instead — allocation-free and O(n)).
	sum := 0
	lo, hi := 0, -1
	out := scratch[:n]
	for i := 0; i < n; i++ {
		wantLo, wantHi := i-half, i+half
		if wantLo < 0 {
			wantLo = 0
		}
		if wantHi > n-1 {
			wantHi = n - 1
		}
		for hi < wantHi {
			hi++
			sum += int(sig[hi])
		}
		for lo < wantLo {
			sum -= int(sig[lo])
			lo++
		}
		out[i] = uint16(math.Round(float64(sum) / float64(wantHi-wantLo+1)))
	}
	for i := 0; i < n; i++ {
		sig[i] = uint8(out[i])
	}
}

// avgRing is the AVERAGE mode state: a true sliding ring of the last N
// published, coherent, edge-aligned frames. Admission is strict — a flat or
// held frame would drag the mean toward the rail. Off-record window columns
// carry NO sample (valid mask false) so they never bias the mean.
type avgRing struct {
	c1, c2 [][]uint8 // aligned windows
	valid  [][]bool  // per-slot per-column contribution mask
	pos    int
	cnt    int
	depth  int
	width  int
}

func (r *avgRing) reset(depth, width int) {
	if depth < 1 {
		depth = 1
	}
	if r.depth != depth || r.width != width || r.c1 == nil {
		r.c1 = make([][]uint8, depth)
		r.c2 = make([][]uint8, depth)
		r.valid = make([][]bool, depth)
		for i := range r.c1 {
			r.c1[i] = make([]uint8, width)
			r.c2[i] = make([]uint8, width)
			r.valid[i] = make([]bool, width)
		}
		r.depth, r.width = depth, width
	}
	r.pos, r.cnt = 0, 0
}

// push aligns the frame so its crossing lands at the window centre (integer
// shift), then accumulates it. Off-record columns are marked invalid, not
// filled with a fabricated code.
func (r *avgRing) push(f *Frame, edgeX float64) {
	shift := int(math.Round(edgeX)) - r.width/2
	c1, c2, vm := r.c1[r.pos], r.c2[r.pos], r.valid[r.pos]
	for i := 0; i < r.width; i++ {
		j := i + shift
		if j < 0 || j >= f.Valid {
			vm[i] = false
			continue
		}
		c1[i], c2[i], vm[i] = f.C1[j], f.C2[j], true
	}
	r.pos = (r.pos + 1) % r.depth
	if r.cnt < r.depth {
		r.cnt++
	}
}

// meanInto replaces the frame's samples with the per-column mean over the
// contributing slots only; a column with no contribution keeps mid-scale.
// The published frame's EdgeX becomes the window centre by construction.
func (r *avgRing) meanInto(f *Frame) {
	if r.cnt == 0 {
		return
	}
	for i := 0; i < r.width; i++ {
		var s1, s2, nc int
		for k := 0; k < r.cnt; k++ {
			if !r.valid[k][i] {
				continue
			}
			s1 += int(r.c1[k][i])
			s2 += int(r.c2[k][i])
			nc++
		}
		if nc == 0 {
			f.C1[i], f.C2[i] = 128, 128
			continue
		}
		f.C1[i] = uint8(s1 / nc)
		f.C2[i] = uint8(s2 / nc)
	}
	f.Valid = r.width
	f.EdgeX = float64(r.width) / 2
}

// uniRing tracks cross-frame per-column uniformity (spec 03 §11): the
// un-fakeable proof that software centring locks the trace. Two variants:
// centred (window extracted around EdgeX) and raw (fixed record position).
const (
	uniDepth = 16
	uniCols  = 256
)

type uniRing struct {
	centred [][]uint8
	raw     [][]uint8
	pos     int
	cnt     int
}

func (u *uniRing) reset() {
	if u.centred == nil {
		u.centred = make([][]uint8, uniDepth)
		u.raw = make([][]uint8, uniDepth)
		for i := range u.centred {
			u.centred[i] = make([]uint8, uniCols)
			u.raw[i] = make([]uint8, uniCols)
		}
	}
	u.pos, u.cnt = 0, 0
}

// push extracts uniCols nearest-sample columns from the WinCols window, once
// centred on edgeX and once at the fixed record centre.
func (u *uniRing) push(disc []uint8, winCols int, edgeX float64) {
	if u.centred == nil {
		u.reset()
	}
	n := len(disc)
	extract := func(dst []uint8, xc float64) {
		left := xc - float64(winCols)/2
		for x := 0; x < uniCols; x++ {
			pos := int(left + float64(x)*float64(winCols)/float64(uniCols))
			if pos < 0 {
				pos = 0
			}
			if pos > n-1 {
				pos = n - 1
			}
			dst[x] = disc[pos]
		}
	}
	xc := edgeX
	if xc < 0 {
		xc = float64(n) / 2
	}
	extract(u.centred[u.pos], xc)
	extract(u.raw[u.pos], float64(n)/2)
	u.pos = (u.pos + 1) % uniDepth
	if u.cnt < uniDepth {
		u.cnt++
	}
}

// stats returns (mean per-column std centred, mean std raw, worst centred
// column std). Aggregation is implementer-defined but stable (spec 03 §11).
func (u *uniRing) stats() (std, stdRaw, worst float64) {
	if u.cnt < 2 {
		return 0, 0, 0
	}
	colStd := func(ring [][]uint8, col int) float64 {
		var s, s2 float64
		for k := 0; k < u.cnt; k++ {
			v := float64(ring[k][col])
			s += v
			s2 += v * v
		}
		m := s / float64(u.cnt)
		v := s2/float64(u.cnt) - m*m
		if v < 0 {
			v = 0
		}
		return math.Sqrt(v)
	}
	var sumC, sumR float64
	for c := 0; c < uniCols; c++ {
		sc := colStd(u.centred, c)
		sumC += sc
		sumR += colStd(u.raw, c)
		if sc > worst {
			worst = sc
		}
	}
	return sumC / uniCols, sumR / uniCols, worst
}
