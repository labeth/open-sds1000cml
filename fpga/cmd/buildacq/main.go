// Command buildacq compiles the standard owned acquisition bitstream. It reads
// the design's Verilog sources (fpga/standard/*.v — acq.v plus the
// spine/capture/envelope/drain/dac module set), the bench-supplied acq.qsf
// (device / pins / IO), and the generated regs.vh / regmux.vh that the RTL
// `include-s, memory-gates the flow, compiles map -> fit -> asm -> cpf in a
// scratch work dir via the pure-Go quartus package, and writes
// fpga/standard/acq.rbf — verifying it is exactly 368011 bytes.
//
// Usage:
//
//	buildacq [flags]
//
// It resolves each source's `include "x.vh" from the design dir (where the
// generated regs.vh / regmux.vh live) and copies them into the work dir so the
// include compiles, and it lists every source as a VERILOG_FILE in the assembled
// QSF (the multi-file generalization).
//
// It is NOT run in CI: it launches headless Quartus 21.1 Lite and is memory-gated
// (the 3.8 GiB bench box runs one flow at a time). Run it on the bench, via
// `make -C fpga bitstream`.
//
// Flags:
//
//	-design DIR   directory holding <name>.v files + <name>.qsf (default "standard")
//	-name NAME    project / top-level entity name (default "acq")
//	-out FILE     bitstream to write (default "<design>/<name>.rbf")
//	-work DIR     scratch work-dir root for the Quartus flow
//	-min-free-mb  RAM floor: refuse to start a flow below it (default 2200)
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"open-sds/fpga/internal/quartus"
)

func main() {
	design := flag.String("design", "standard", "directory holding <name>.v files and <name>.qsf")
	name := flag.String("name", "acq", "project / top-level entity name")
	out := flag.String("out", "", "bitstream to write (default \"<design>/<name>.rbf\")")
	work := flag.String("work", "", "scratch work-dir root for the Quartus flow (default \"~/quartus_work/buildacq\")")
	minFreeMB := flag.Int("min-free-mb", 2200, "RAM floor: refuse to start a Quartus flow below this many MB free")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(),
			"usage: buildacq [flags]\n\n"+
				"Compiles <design>/<name>.v* + <name>.qsf + the generated regs.vh/regmux.vh\n"+
				"-> <design>/<name>.rbf (exactly %d bytes). Needs Quartus; NOT run in CI.\n\n",
			quartus.RBFBytes)
		flag.PrintDefaults()
	}
	flag.Parse()

	if flag.NArg() != 0 {
		flag.Usage()
		os.Exit(2)
	}
	if *work == "" {
		*work = defaultWork()
	}
	if err := run(*name, *design, *out, *work, *minFreeMB); err != nil {
		fmt.Fprintf(os.Stderr, "buildacq: %v\n", err)
		os.Exit(1)
	}
}

func run(name, design, out, work string, minFreeMB int) error {
	dir, err := resolveDesign(design, name)
	if err != nil {
		return err
	}

	sources, err := readSources(dir)
	if err != nil {
		return err
	}
	qsf, err := os.ReadFile(filepath.Join(dir, name+".qsf"))
	if err != nil {
		return fmt.Errorf("reading QSF: %w", err)
	}
	// Resolve every `include "x.vh" (the generated regs.vh / regmux.vh, which
	// live in the design dir) so the SSOT interface headers compile in the copied
	// work dir.
	includes, err := quartus.ResolveIncludes(sources, dir)
	if err != nil {
		return err
	}

	if out == "" {
		out = filepath.Join(dir, name+".rbf")
	}

	// Never run two Quartus flows at once — the 3.8 GiB box OOMs (see GateReady).
	if err := quartus.GateReady(minFreeMB); err != nil {
		return err
	}

	fmt.Printf("buildacq: compiling %s (%d source%s, %d include%s) -> %s\n"+
		"          map/fit/asm/cpf via headless Quartus — this takes a while...\n",
		name, len(sources), plural(len(sources)), len(includes), plural(len(includes)), out)

	p := quartus.Project{
		Name:     name,
		Top:      name,
		QSF:      string(qsf),
		Sources:  sources,
		Includes: includes,
	}
	rbf, err := quartus.New(work).Compile(p, out)
	if err != nil {
		return err
	}

	fi, err := os.Stat(rbf)
	if err != nil {
		return fmt.Errorf("stat rbf: %w", err)
	}
	if fi.Size() != quartus.RBFBytes {
		return fmt.Errorf("%s is %d bytes, want %d", rbf, fi.Size(), quartus.RBFBytes)
	}
	fmt.Printf("OK: %s (%d bytes)\n", rbf, fi.Size())
	return nil
}

// readSources reads every *.v file in dir (sorted, for a deterministic
// VERILOG_FILE order) as a Verilog source.
func readSources(dir string) ([]quartus.Source, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.v"))
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	var srcs []quartus.Source
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		srcs = append(srcs, quartus.Source{Name: filepath.Base(p), Content: string(data)})
	}
	if len(srcs) == 0 {
		return nil, fmt.Errorf("no *.v design files found in %s", dir)
	}
	return srcs, nil
}

// resolveDesign returns the directory that actually holds <name>.qsf. It tries
// the given path, then — for a relative path — the same path one and two levels
// up, so the tool works from the fpga module root (`go run ./cmd/buildacq`,
// default "standard") and from a nested cwd.
func resolveDesign(design, name string) (string, error) {
	cands := []string{design}
	if !filepath.IsAbs(design) {
		cands = append(cands, filepath.Join("..", design), filepath.Join("..", "..", design))
	}
	for _, d := range cands {
		if _, err := os.Stat(filepath.Join(d, name+".qsf")); err == nil {
			return d, nil
		}
	}
	return "", fmt.Errorf("cannot find %s.qsf under %s", name, strings.Join(cands, " or "))
}

func defaultWork() string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, "quartus_work", "buildacq")
	}
	return filepath.Join(os.TempDir(), "quartus_work", "buildacq")
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
