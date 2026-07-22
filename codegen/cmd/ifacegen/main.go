// Command ifacegen validates the owned FPGA<->app interface schema and emits the
// four generated artifacts from text/template sources:
//
//	<go>/iface.go          the app's Go bindings          (default ../app/internal/iface)
//	<vh>/regs.vh           Verilog `define register header (default ../fpga/standard)
//	<vh>/regmux.vh         Verilog decode include         (default ../fpga/standard)
//	<doc>/REGISTER-MAP.md  the register-map reference      (default ../fpga/standard/docs)
//
// With -check it regenerates all four in memory and fails (exit 1) if any
// checked-in copy differs — the CI drift gate that keeps fabric and app in
// lockstep. Paths are resolved relative to the codegen module root.
//
//	go run ./cmd/ifacegen                # (re)generate into the trees above
//	go run ./cmd/ifacegen -check         # fail if any generated file is stale
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"open-sds/codegen/emit"
	"open-sds/codegen/ifacedef"
	"open-sds/codegen/schema"
)

func main() {
	goDir := flag.String("go", "../app/internal/iface", "output dir for the Go bindings (iface.go)")
	vhDir := flag.String("vh", "../fpga/standard", "output dir for regs.vh + regmux.vh")
	docDir := flag.String("doc", "../fpga/standard/docs", "output dir for REGISTER-MAP.md")
	check := flag.Bool("check", false, "verify checked-in files match (drift gate); no writes")
	flag.Parse()

	iface := ifacedef.Standard()
	if errs := iface.Validate(); len(errs) > 0 {
		fmt.Fprintf(os.Stderr, "schema INVALID (%d problem(s)):\n", len(errs))
		for _, e := range errs {
			fmt.Fprintf(os.Stderr, "  - %v\n", e)
		}
		os.Exit(2)
	}

	regs, err := emit.Verilog(iface)
	must(err)
	regmux, err := emit.Regmux(iface)
	must(err)
	bindings, err := emit.GoBindings(iface)
	must(err)
	doc, err := emit.Doc(iface)
	must(err)

	// Ordered so output is deterministic.
	targets := []struct{ path, content string }{
		{filepath.Join(*goDir, "iface.go"), bindings},
		{filepath.Join(*vhDir, "regs.vh"), regs},
		{filepath.Join(*vhDir, "regmux.vh"), regmux},
		{filepath.Join(*docDir, "REGISTER-MAP.md"), doc},
	}

	if *check {
		stale := false
		for _, t := range targets {
			got, err := os.ReadFile(t.path)
			if err != nil || string(got) != t.content {
				fmt.Fprintf(os.Stderr, "DRIFT: %s is stale or missing — run `make generate`\n", t.path)
				stale = true
			}
		}
		if stale {
			os.Exit(1)
		}
		fmt.Printf("ok: generated files up to date (build-ID 0x%08x, %d registers)\n", iface.BuildID(), len(iface.AllRegs()))
		return
	}

	for _, t := range targets {
		if err := os.MkdirAll(filepath.Dir(t.path), 0o755); err != nil {
			fatal(err)
		}
		if err := os.WriteFile(t.path, []byte(t.content), 0o644); err != nil {
			fatal(err)
		}
		fmt.Printf("wrote %s\n", t.path)
	}
	fmt.Printf("build-ID 0x%08x, %d registers, %d channels\n", iface.BuildID(), len(iface.AllRegs()), len(iface.Channels))
	_ = schema.CS1
}

func must(err error) {
	if err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
