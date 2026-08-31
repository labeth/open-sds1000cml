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

// The default output dirs, relative to the codegen module root. They are named
// constants (not just flag defaults) because the drift TEST renders the same
// target list from its own working directory — one source of truth for WHICH
// files are generated, so a new artifact cannot be added to the tool and forgotten
// by the gate.
const (
	DefaultGoDir  = "../app/internal/iface"
	DefaultVhDir  = "../fpga/standard"
	DefaultDocDir = "../fpga/standard/docs"
)

// target is one checked-in generated file and the content it must have.
type target struct {
	path    string
	content string
}

// targets renders every generated artifact in a deterministic order. Both the
// generator, the -check drift gate and the drift test go through here.
func targets(iface schema.Interface, goDir, vhDir, docDir string) ([]target, error) {
	regs, err := emit.Verilog(iface)
	if err != nil {
		return nil, err
	}
	regmux, err := emit.Regmux(iface)
	if err != nil {
		return nil, err
	}
	bindings, err := emit.GoBindings(iface)
	if err != nil {
		return nil, err
	}
	doc, err := emit.Doc(iface)
	if err != nil {
		return nil, err
	}
	return []target{
		{filepath.Join(goDir, "iface.go"), bindings},
		{filepath.Join(vhDir, "regs.vh"), regs},
		{filepath.Join(vhDir, "regmux.vh"), regmux},
		{filepath.Join(docDir, "REGISTER-MAP.md"), doc},
	}, nil
}

func main() {
	goDir := flag.String("go", DefaultGoDir, "output dir for the Go bindings (iface.go)")
	vhDir := flag.String("vh", DefaultVhDir, "output dir for regs.vh + regmux.vh")
	docDir := flag.String("doc", DefaultDocDir, "output dir for REGISTER-MAP.md")
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

	tgts, err := targets(iface, *goDir, *vhDir, *docDir)
	must(err)

	if *check {
		stale := false
		for _, t := range tgts {
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

	for _, t := range tgts {
		if err := os.MkdirAll(filepath.Dir(t.path), 0o755); err != nil {
			fatal(err)
		}
		if err := os.WriteFile(t.path, []byte(t.content), 0o644); err != nil {
			fatal(err)
		}
		fmt.Printf("wrote %s\n", t.path)
	}
	fmt.Printf("build-ID 0x%08x, %d registers, %d channels\n", iface.BuildID(), len(iface.AllRegs()), len(iface.Channels))
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
