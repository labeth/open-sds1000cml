//go:build ignore

// Generates the UI palette from tokens.json into TWO artifacts so web and the
// on-device LCD share one source of truth:
//   - tokens.css            : the web :root custom properties
//   - ../lcd/palette_gen.go  : the LCD col* variables (rgb(r,g,b) → RGB565)
//
// Run: go generate ./internal/web   (or: cd internal/web && go run gen_tokens.go)
// A golden test (palette_parity_test.go) asserts the two agree color-for-color
// and that the committed files match this generator, so drift is unmergeable.
package main

import (
	"encoding/json"
	"fmt"
	"go/format"
	"os"
	"sort"
	"strings"
)

type spec struct {
	Tokens map[string]string `json:"tokens"`
	LCD    map[string]string `json:"lcd"`
}

func hexRGB(h string) (r, g, b uint8) {
	h = strings.TrimPrefix(h, "#")
	var v uint64
	fmt.Sscanf(h, "%06x", &v)
	return uint8(v >> 16), uint8(v >> 8), uint8(v)
}

func main() {
	raw, err := os.ReadFile("tokens.json")
	must(err)
	var s spec
	must(json.Unmarshal(raw, &s))

	// tokens.css — :root vars, sorted for deterministic output.
	names := make([]string, 0, len(s.Tokens))
	for n := range s.Tokens {
		names = append(names, n)
	}
	sort.Strings(names)
	var css strings.Builder
	css.WriteString("/* Code generated from tokens.json by gen_tokens.go. DO NOT EDIT. */\n:root {\n")
	for _, n := range names {
		fmt.Fprintf(&css, "  --%s: %s;\n", n, strings.ToLower(s.Tokens[n]))
	}
	css.WriteString("}\n")
	must(os.WriteFile("tokens.css", []byte(css.String()), 0o644))

	// ../lcd/palette_gen.go — col* vars from the lcd→token mapping.
	cols := make([]string, 0, len(s.LCD))
	for c := range s.LCD {
		cols = append(cols, c)
	}
	sort.Strings(cols)
	var go_ strings.Builder
	go_.WriteString("// Code generated from ../web/tokens.json by gen_tokens.go. DO NOT EDIT.\n")
	go_.WriteString("// The LCD palette shares web's tokens so the two surfaces cannot diverge.\n\n")
	go_.WriteString("package lcd\n\nvar (\n")
	for _, c := range cols {
		tok := s.LCD[c]
		hex, ok := s.Tokens[tok]
		if !ok {
			fmt.Fprintf(os.Stderr, "lcd %s references unknown token %q\n", c, tok)
			os.Exit(1)
		}
		r, g, b := hexRGB(hex)
		fmt.Fprintf(&go_, "\t%s = rgb(%d, %d, %d) // --%s %s\n", c, r, g, b, tok, hex)
	}
	go_.WriteString(")\n")
	formatted, err := format.Source([]byte(go_.String()))
	must(err)
	must(os.WriteFile("../lcd/palette_gen.go", formatted, 0o644))

	fmt.Printf("generated tokens.css (%d tokens) + ../lcd/palette_gen.go (%d cols)\n", len(names), len(cols))
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
