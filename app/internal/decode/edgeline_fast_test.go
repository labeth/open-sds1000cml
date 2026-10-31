// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"fmt"
	"math"
	"math/rand"
	"strconv"
	"testing"
)

// hexText is fmt's "%s%0*X" of a non-negative value, every width.
// TRLC-LINKS: REQ-SDS-018
func TestHexTextIsSprintf(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for it := 0; it < 20000; it++ {
		v := uint64(rng.Int63()) >> uint(rng.Intn(64))
		if it%7 == 0 {
			v = 0
		}
		digits := rng.Intn(10)
		pre := []string{"", "!", "TDO", "DR"}[rng.Intn(4)]
		if got, want := hexText(pre, v, digits), fmt.Sprintf("%s%0*X", pre, digits, v); got != want {
			t.Fatalf("hexText(%q, %d, %d) = %q, want %q", pre, v, digits, got, want)
		}
	}
}

// FmtByte's tables are the texts it formatted each time.
// TRLC-LINKS: REQ-SDS-018
func TestFmtByteTables(t *testing.T) {
	for v := -300; v < 600; v++ {
		b := v & 0xff
		want := map[string]string{"dec": fmt.Sprint(b), "bin": fmt.Sprintf("%08b", b), "": hex2(v)}
		if v >= 32 && v < 127 {
			want["ascii"], want["both"] = string(rune(v)), hex2(v)+"'"+string(rune(v))
		} else {
			want["ascii"], want["both"] = ".", hex2(v)
		}
		for f, w := range want {
			if got := FmtByte(v, f); got != w {
				t.Fatalf("FmtByte(%d, %q) = %q, want %q", v, f, got, w)
			}
		}
	}
	for d := 0; d <= 1000; d++ {
		if got, want := pwmDutyTexts()[d], strconv.Itoa(d/10)+"."+strconv.Itoa(d%10)+"%"; got != want {
			t.Fatalf("duty %d: %q, want %q", d, got, want)
		}
	}
}

// nthNear is nthValue for values within a hair of their integers: gaps
// alike, ones off by rounding either way, integers spread past the
// histogram, the same working arrays from call to call.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-236
func TestNthNearIsNthValue(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	var b nearBuf
	for it := 0; it < 5000; it++ {
		n := 1 + rng.Intn([]int{5, 50, 3000}[rng.Intn(3)])
		v, iv := make([]float64, n), make([]int, n)
		base := []int{1, 8, 1 << 20}[rng.Intn(3)]
		spread := []int{1, 3, 100, 100000}[rng.Intn(4)]
		for j := range v {
			d := base + rng.Intn(spread)
			iv[j] = d
			switch rng.Intn(4) {
			case 0:
				v[j] = float64(d)
			case 1:
				v[j] = float64(d) + 1e-9*rng.Float64()
			case 2:
				v[j] = float64(d) - 1e-9*rng.Float64()
			default:
				v[j] = math.Nextafter(float64(d), math.Inf(2*rng.Intn(2)-1))
			}
		}
		k := rng.Intn(n)
		want := nthValue(v, k)
		if got := nthNear(v, iv, k, &b); math.Float64bits(got) != math.Float64bits(want) {
			t.Fatalf("n %d k %d: %v, want %v", n, k, got, want)
		}
		if got := nthNear(v, iv, k, nil); math.Float64bits(got) != math.Float64bits(want) {
			t.Fatalf("n %d k %d without buffers: %v, want %v", n, k, got, want)
		}
	}
}

// orderKey orders as the values do, and keyValue gives each back.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-254
func TestOrderKey(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	vals := []float64{0, 1, -1, 0.5, -0.5, math.Inf(1), math.Inf(-1), math.MaxFloat64, -math.MaxFloat64, 5e-324, -5e-324}
	for range 2000 {
		vals = append(vals, (rng.Float64()-0.5)*math.Pow(10, float64(rng.Intn(40)-20)))
	}
	for _, x := range vals {
		if y := keyValue(orderKey(x)); math.Float64bits(y) != math.Float64bits(x) {
			t.Fatalf("%v back as %v", x, y)
		}
		for _, z := range vals {
			if (x < z) != (orderKey(x) < orderKey(z)) {
				t.Fatalf("%v < %v: keys disagree", x, z)
			}
		}
	}
}

// levelCursor is logicAt wherever it is asked, forward or back.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-236
func TestLevelCursorIsLogicAt(t *testing.T) {
	rng := rand.New(rand.NewSource(4))
	for it := 0; it < 300; it++ {
		n := 1 + rng.Intn(3000)
		l := edgeLineOf(randomLevels(rng, n, []float64{1, 4, 60}[rng.Intn(3)], rng.Intn(4) == 0))
		s := l.slice(128, true)
		if !s.ok {
			continue
		}
		c := levelCursor{s: &s}
		x := rng.Float64() * float64(n)
		for range 200 {
			if rng.Intn(5) == 0 {
				x = rng.Float64()*float64(n+4) - 2 // anywhere
			} else {
				x += rng.Float64()*8 - 2 // mostly on
			}
			if got, want := c.at(x), logicAt(&s, x); got != want {
				t.Fatalf("at(%v) %d, want %d", x, got, want)
			}
		}
	}
}

// roundUps is what math.Round adds to each crossing of a line sliced from
// its edges, at thresholds below, on and a hair off the half.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-236
func TestRoundUpsIsRound(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	thrs := []float64{128, 127.5, 127.5 + 1e-12, 127.5 - 1e-12, 127.5 + 1e-3, 30, 228, 90.25}
	tried := 0
	for it := 0; it < 200; it++ {
		l := edgeLineOf(randomLevels(rng, 1+rng.Intn(20000), 5, false))
		for _, thr := range thrs {
			s := l.slice(thr, true)
			fall, rise, ok := s.roundUps()
			if !ok {
				continue
			}
			tried++
			for _, e := range s.edges {
				up := rise
				if e.dir < 0 {
					up = fall
				}
				if want := int(math.Round(e.x)); e.i-1+up != want {
					t.Fatalf("thr %v edge at %d (x %v): %d, want %d", thr, e.i, e.x, e.i-1+up, want)
				}
			}
		}
	}
	if tried == 0 {
		t.Fatal("no line sliced from its edges")
	}
}

// nearBoundOf puts an integer clear of a bound only where every value
// within a hair of it lies on that side.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-236
func TestNearBound(t *testing.T) {
	rng := rand.New(rand.NewSource(6))
	for range 20000 {
		bnd := float64(rng.Intn(200)) + []float64{0, 0.5, 1e-9, -1e-9, rng.Float64()}[rng.Intn(5)]
		b := nearBoundOf(bnd)
		for gi := int(bnd) - 3; gi <= int(bnd)+3; gi++ {
			for _, e := range []float64{-1e-7, 0, 1e-7} {
				g := float64(gi) + e
				if gi <= b.lo && !(g < bnd) || gi >= b.hi && !(g > bnd) {
					t.Fatalf("bound %v: %d (%v) misplaced by %+v", bnd, gi, g, b)
				}
			}
		}
	}
}

// A scratch lends the arrays of one decode apart, the same ones to the
// next, and keeps the larger array an append grows.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func TestScratch(t *testing.T) {
	var sc scratch
	a := scratchSlice[int](&sc, 10)[:10]
	b := scratchSlice[int](&sc, 10)[:10]
	if &a[0] == &b[0] {
		t.Fatal("one decode's two arrays share")
	}
	sc.reset()
	if a2 := scratchSlice[int](&sc, 5); &a2[:1][0] != &a[0] {
		t.Fatal("the next decode's first array is not the first one's")
	}
	s := scratchSlice[float64](&sc, 1)
	for k := range 1000 {
		s = scratchAppend(&sc, 1, s, float64(k))
	}
	sc.reset()
	scratchSlice[int](&sc, 1)
	if s2 := scratchSlice[float64](&sc, 1000); cap(s2) < 1000 || &s2[:1][0] != &s[0] {
		t.Fatal("the grown array was not kept")
	}
}
