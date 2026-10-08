// ENGMODEL-OWNER-UNIT: FU-APP-SUPERRES
package superres

import (
	"fmt"
	"math/big"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TRLC-LINKS: REQ-SDS-141
func TestStackTileTransferRTL(t *testing.T) {
	rng := rand.New(rand.NewSource(1414))
	var input strings.Builder
	expected := []string{}
	var memory [64]*big.Int
	clear := func() {
		for i := range memory {
			memory[i] = new(big.Int)
		}
	}
	clear()
	emit := func(write bool, bin uint32, ch, abort int, badPadding bool) {
		w := 0
		if write {
			w = 1
		}
		data := new(big.Int).Rand(rng, new(big.Int).Lsh(big.NewInt(1), 308))
		if badPadding {
			data.SetBit(data, 319, 1)
		}
		fmt.Fprintf(&input, "%d %d %d %080x %d %d\n", w, bin, ch, data, abort, len(expected)%4)
		err := 0
		out := new(big.Int)
		if abort != 0 {
			clear()
		} else if bin >= 32 || write && badPadding {
			err = 1
		} else if write {
			memory[int(bin)*2+ch].Set(data)
		} else {
			out.Set(memory[int(bin)*2+ch])
		}
		expected = append(expected, fmt.Sprintf("%d %x", err, out))
	}
	for b := uint32(0); b < 32; b++ {
		for ch := 0; ch < 2; ch++ {
			emit(false, b, ch, 0, false)
			emit(true, b, ch, 0, false)
			emit(false, b, ch, 0, false)
		}
	}
	for i := 0; i < 80; i++ {
		b, ch := uint32(rng.Intn(32)), rng.Intn(2)
		emit(true, b, ch, 0, true)
		emit(false, b, ch, 0, false)
	}
	for _, b := range []uint32{32, 33, 1 << 31, ^uint32(0)} {
		emit(true, b, 1, 0, false)
		emit(false, b, 0, 0, false)
	}
	for _, n := range []int{1, 10, 19} {
		emit(true, 12, 1, n, false)
		for b := uint32(0); b < 32; b++ {
			emit(false, b, 0, 0, false)
			emit(false, b, 1, 0, false)
		}
		emit(true, 25, 1, 0, false)
		emit(false, 25, 1, 0, false)
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "input.txt")
	if err := os.WriteFile(p, []byte(input.String()), 0600); err != nil {
		t.Fatal(err)
	}
	image := compileStackRTL(t, dir, "tb_stack_tile_transfer", "stack_tile_transfer.v", "stack_state_tile.v", "sim/tb_stack_tile_transfer.v")
	out, err := exec.Command("vvp", image, "+input="+p).CombinedOutput()
	if err != nil {
		t.Fatalf("simulation %v\n%s", err, out)
	}
	n := 0
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.HasPrefix(line, "X ") {
			continue
		}
		fields := strings.Fields(line)
		v, ok := new(big.Int).SetString(fields[2], 16)
		if !ok {
			t.Fatalf("invalid payload %s", line)
		}
		got := fields[1] + " " + v.Text(16)
		if n >= len(expected) {
			t.Fatal("extra response")
		}
		if got != expected[n] {
			t.Fatalf("fixture %d got %s want %s", n, got, expected[n])
		}
		n++
	}
	if n != len(expected) {
		t.Fatalf("results %d want %d", n, len(expected))
	}
	t.Logf("%d host transfers: exact 20-halfword round trips, padding rejection, bounds, stalls and partial-upload resets passed", n)
}

// TRLC-LINKS: REQ-SDS-141
func TestStackTileAccessRTL(t *testing.T) {
	dir := t.TempDir()
	image := compileStackRTL(t, dir, "tb_stack_tile_access", "stack_tile_access.v", "stack_tile_transfer.v", "stack_state_tile.v", "sim/tb_stack_tile_access.v")
	out, err := exec.Command("vvp", image).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "PASS exclusive ownership") {
		t.Fatalf("ownership simulation: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}
