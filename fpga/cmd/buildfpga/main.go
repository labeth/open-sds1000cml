// Command buildfpga compiles one per-design folder of the fpga module into a
// bitstream with headless Quartus 21.1 Lite:
//
//	go run ./cmd/buildfpga -design default   ->  fpga/default/out/default.rbf
//
// It stages fpga/common/*.v + fpga/<design>/{*.v,*.vh,.qsf,.sdc} into
// fpga/<design>/out (git-ignored), takes flock /tmp/open-sds-quartus.lock,
// refuses to start below 3 GB of free RAM, runs map -> fit -> sta -> asm -> cpf
// with bitstream compression off, checks the rbf is exactly 368011 bytes, and
// prints a summary: resource usage, every QSF ball's placement defect, and the
// worst setup slack per clock. Report-level defects (an unassigned or unused
// pin, negative slack, no timing constraints) exit 1 even though the rbf was
// written -- 05-WORKPLAN section 4.9 makes them build failures.
//
// NOT run in CI; needs Quartus on this host. Never starts a second flow.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"open-sds/fpga/internal/quartus"
)

func main() {
	design := flag.String("design", "default", "design folder under fpga/ (project, revision and rbf name)")
	fpgaDir := flag.String("fpga-dir", "", "the fpga module root (default: found from the working directory)")
	root := flag.String("quartus", "", "Quartus install root (default $QUARTUS_ROOTDIR or the pinned 21.1 Lite path)")
	lock := flag.String("lock", quartus.DefaultLockPath, "flock file serializing Quartus flows from this repo")
	lockWait := flag.Duration("lock-wait", 0, "how long to wait for the lock (0 = fail at once if another flow holds it)")
	minFree := flag.Int("min-free-mb", quartus.DefaultMinFreeMB, "MemAvailable floor in MB; -1 disables the gate")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "usage: buildfpga [flags]\n\nCompiles fpga/<design> -> fpga/<design>/out/<design>.rbf (exactly %d bytes).\n\n", quartus.RBFBytes)
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() != 0 {
		flag.Usage()
		os.Exit(2)
	}

	if *fpgaDir == "" {
		d, err := findModuleRoot()
		if err != nil {
			fmt.Fprintln(os.Stderr, "buildfpga:", err)
			os.Exit(2)
		}
		*fpgaDir = d
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	c := quartus.Config{
		Root:      *root,
		FPGADir:   *fpgaDir,
		Design:    *design,
		LockPath:  *lock,
		LockWait:  *lockWait,
		MinFreeMB: *minFree,
		Log:       os.Stdout,
	}
	fmt.Printf("buildfpga: design %s in %s (Quartus %s)\n", c.Design, c.FPGADir, orDefault(c.Root, quartus.DefaultRoot()))
	t0 := time.Now()
	res, err := quartus.Run(ctx, c)
	if res != nil {
		printSummary(res)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "buildfpga: FAILED after %s: %v\n", time.Since(t0).Round(time.Second), err)
		os.Exit(1)
	}
	defects := res.Defects()
	if len(defects) > 0 {
		fmt.Printf("\nDEFECTS (%d) -- the rbf was written but this build does not pass 05-WORKPLAN section 4.9:\n", len(defects))
		for _, d := range defects {
			fmt.Println("  - " + d)
		}
		os.Exit(1)
	}
	fmt.Printf("\nOK: %s (%d bytes) in %s\n", res.RBF, res.RBFSize, time.Since(t0).Round(time.Second))
}

func printSummary(res *quartus.Result) {
	fmt.Println()
	fmt.Println("stages:")
	for _, s := range res.Stages {
		fmt.Printf("  %-12s %8s  %s\n", s.Tool, s.Duration.Round(time.Second), s.LogFile)
	}
	if res.RBF != "" {
		fmt.Printf("rbf: %s (%d bytes, want %d)\n", res.RBF, res.RBFSize, quartus.RBFBytes)
	}
	if len(res.Fit.Summary) > 0 {
		fmt.Println("fitter:")
		keys := make([]string, 0, len(res.Fit.Summary))
		for k := range res.Fit.Summary {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Printf("  %-36s %s\n", k, res.Fit.Summary[k])
		}
	}
	if len(res.Fit.Warnings) > 0 {
		fmt.Println("fitter I/O messages:")
		for _, w := range res.Fit.Warnings {
			fmt.Println("  " + w)
		}
	}
	if res.FitRpt != "" {
		fmt.Printf("pins: %d placement problems against the QSF (report %s)\n", len(res.Fit.Problems), res.FitRpt)
		for _, p := range res.Fit.Problems {
			fmt.Println("  - " + p.String())
		}
	}
	if res.STARpt != "" {
		fmt.Printf("timing: %d setup-summary tables (report %s)\n", res.Timing.Tables, res.STARpt)
		for _, c := range res.Timing.Clocks() {
			mark := "ok  "
			if res.Timing.WorstSetup[c] < 0 {
				mark = "FAIL"
			}
			fmt.Printf("  %s  %-40s worst setup slack %8.3f ns  (%s)\n", mark, c, res.Timing.WorstSetup[c], res.Timing.Corner[c])
		}
	}
}

// findModuleRoot walks up from the working directory to the directory holding
// the fpga module's go.mod (so `go run ./cmd/buildfpga` works from fpga/ and
// from any folder beneath it).
func findModuleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err == nil && strings.HasPrefix(strings.TrimSpace(string(data)), "module open-sds/fpga") {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("not inside the open-sds/fpga module; pass -fpga-dir")
		}
		dir = parent
	}
}

func orDefault(v, d string) string {
	if v == "" {
		return d
	}
	return v
}
