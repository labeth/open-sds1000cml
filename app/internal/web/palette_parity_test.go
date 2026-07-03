package web

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Pure-Go golden guardrail (always runs — no node/Playwright): the web palette
// (tokens.css) and the LCD palette (../lcd/palette_gen.go) must both agree,
// color-for-color, with the single source (tokens.json). This makes the verified
// #1 bug — trigger red on web vs green on the LCD — permanently unmergeable, and
// also fails if tokens.json changed without `go generate ./internal/web`.

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return string(b)
}

var cssVarRe = regexp.MustCompile(`--([\w-]+):\s*(#[0-9a-fA-F]{6})`)
var palRe = regexp.MustCompile(`(col\w+)\s*=\s*rgb\((\d+),\s*(\d+),\s*(\d+)\)`)

func hex2rgb(h string) [3]int {
	v, _ := strconv.ParseInt(strings.TrimPrefix(h, "#"), 16, 64)
	return [3]int{int(v>>16) & 0xff, int(v>>8) & 0xff, int(v) & 0xff}
}

func TestPaletteParity(t *testing.T) {
	var src struct {
		Tokens map[string]string `json:"tokens"`
		LCD    map[string]string `json:"lcd"`
	}
	if err := json.Unmarshal([]byte(read(t, "tokens.json")), &src); err != nil {
		t.Fatal(err)
	}

	// tokens.css == tokens.json (web consumes the generated file).
	css := map[string]string{}
	for _, m := range cssVarRe.FindAllStringSubmatch(read(t, "tokens.css"), -1) {
		css[m[1]] = strings.ToLower(m[2])
	}
	if len(css) != len(src.Tokens) {
		t.Errorf("tokens.css has %d vars but tokens.json has %d — run `go generate ./internal/web`", len(css), len(src.Tokens))
	}
	for name, hex := range src.Tokens {
		if got := css[name]; got != strings.ToLower(hex) {
			t.Errorf("tokens.css --%s = %q, source is %q — regenerate", name, got, strings.ToLower(hex))
		}
	}

	// ../lcd/palette_gen.go colX == tokens.json[lcd[colX]] (LCD shares the source
	// ⇒ web and LCD cannot diverge).
	pal := map[string][3]int{}
	for _, m := range palRe.FindAllStringSubmatch(read(t, "../lcd/palette_gen.go"), -1) {
		r, _ := strconv.Atoi(m[2])
		g, _ := strconv.Atoi(m[3])
		b, _ := strconv.Atoi(m[4])
		pal[m[1]] = [3]int{r, g, b}
	}
	if len(pal) != len(src.LCD) {
		t.Errorf("palette_gen.go has %d cols but tokens.json lcd map has %d — regenerate", len(pal), len(src.LCD))
	}
	for col, tok := range src.LCD {
		hex, ok := src.Tokens[tok]
		if !ok {
			t.Errorf("lcd %s → unknown token %q", col, tok)
			continue
		}
		want := hex2rgb(hex)
		if got := pal[col]; got != want {
			t.Errorf("LCD %s = rgb%v but token --%s (%s) = rgb%v — web/LCD diverged, run `go generate ./internal/web`",
				col, got, tok, hex, want)
		}
	}
	if t.Failed() {
		return
	}
	t.Logf("palette parity OK: %d web tokens, %d shared LCD colors, single source %s",
		len(src.Tokens), len(src.LCD), fmt.Sprintf("tokens.json"))
}
