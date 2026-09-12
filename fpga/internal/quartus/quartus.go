// Package quartus drives Intel Quartus Prime 21.1 Lite headlessly for one
// per-design folder of the fpga module: it stages fpga/common/*.v plus
// fpga/<design>/{*.v,*.vh,<design>.qsf,<design>.sdc} flat into
// fpga/<design>/out/, runs quartus_map -> quartus_fit -> quartus_sta ->
// quartus_asm -> quartus_cpf (bitstream_compression=off), checks the .rbf is
// exactly RBFBytes, and parses the fit report (unassigned / unused pins on the
// QSF's balls) and the STA report (worst setup slack per clock).
//
// Rules it enforces (docs/acq2/05-WORKPLAN.md section 4.6, 4.9):
//   - one flow at a time from this repo: flock on LockPath, held for the whole flow
//   - never start below MinFreeMB of MemAvailable (a foreign quartus_map shares this host)
//   - the QSF is generated: its VERILOG_FILE list must equal the staged .v set, and the
//     SDC it names must exist (an unconstrained STA would silently pass)
//
// Idea credit: the map/fit/asm/cpf sequence, the memory gate and the 368011-byte
// check follow open-sds1000cml:owned-fpga fpga/internal/quartus (commit 6afff71);
// this driver adds the lock, the STA stage, the report parsing and the
// staged-directory model (no scratch copy under $HOME).
//
// The package is unit-tested with fake quartus_* shell scripts; it never runs
// the real tools in its tests.
package quartus

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// RBFBytes is the size of an uncompressed EP4CE10F17C8 bitstream
// (01-CONTRACT section 0: 368,011 B). Anything else means compression was left
// on or the device changed, and the loader would push garbage.
const RBFBytes = 368011

// DefaultLockPath serializes every Quartus flow started from this repo.
const DefaultLockPath = "/tmp/open-sds-quartus.lock"

// DefaultMinFreeMB is the MemAvailable floor below which no flow starts.
const DefaultMinFreeMB = 3 * 1024

// DefaultRoot returns $QUARTUS_ROOTDIR or the pinned 21.1 Lite install.
func DefaultRoot() string {
	if r := os.Getenv("QUARTUS_ROOTDIR"); r != "" {
		return r
	}
	return "/home/labeth/intelFPGA_lite/21.1/quartus"
}

// Config describes one flow.
type Config struct {
	Root      string // Quartus install root (bin/ underneath); DefaultRoot() if empty
	FPGADir   string // the fpga module root (holds common/ and <Design>/)
	Design    string // design folder + project + revision name, e.g. "default"
	OutDir    string // work dir; FPGADir/<Design>/out if empty
	LockPath  string // DefaultLockPath if empty
	LockWait  time.Duration
	MinFreeMB int       // DefaultMinFreeMB if 0; <0 disables the gate
	MemInfo   string    // /proc/meminfo if empty (tests point it elsewhere)
	Log       io.Writer // progress log; io.Discard if nil
}

// Stage records one tool invocation.
type Stage struct {
	Tool     string
	Args     []string
	Duration time.Duration
	LogFile  string
}

// Result is the outcome of a successful flow (every tool exited 0 and the rbf
// has the right size). Defects the reports show are carried in Fit.Problems
// and Timing.Failing(); the caller decides whether they fail the build.
type Result struct {
	RBF     string
	RBFSize int64
	Stages  []Stage
	Sources []string // staged .v files, in QSF order
	Fit     FitReport
	Timing  TimingReport
	FitRpt  string
	STARpt  string
}

// Defects lists everything the caller should treat as a failed build even
// though the tools exited 0.
func (r Result) Defects() []string {
	var d []string
	for _, p := range r.Fit.Problems {
		d = append(d, "pin "+p.String())
	}
	if r.Timing.Tables == 0 {
		d = append(d, "timing: no Setup Summary table in the STA report (no SDC constraints applied?)")
	}
	for _, c := range r.Timing.Failing() {
		d = append(d, fmt.Sprintf("timing: clock %s worst setup slack %.3f ns (%s)", c, r.Timing.WorstSetup[c], r.Timing.Corner[c]))
	}
	return d
}

func (c *Config) fill() error {
	if c.Root == "" {
		c.Root = DefaultRoot()
	}
	if c.FPGADir == "" || c.Design == "" {
		return errors.New("quartus: FPGADir and Design are required")
	}
	if c.OutDir == "" {
		c.OutDir = filepath.Join(c.FPGADir, c.Design, "out")
	}
	if c.LockPath == "" {
		c.LockPath = DefaultLockPath
	}
	if c.MinFreeMB == 0 {
		c.MinFreeMB = DefaultMinFreeMB
	}
	if c.MemInfo == "" {
		c.MemInfo = "/proc/meminfo"
	}
	if c.Log == nil {
		c.Log = io.Discard
	}
	return nil
}

// --- lock ---------------------------------------------------------------------

// Lock is a held flock on the serialization file.
type Lock struct{ f *os.File }

// AcquireLock takes an exclusive flock on path, waiting up to wait (0 = try
// once). The holder's pid and design are written into the file for
// diagnostics; a foreign holder is reported in the error.
func AcquireLock(ctx context.Context, path string, wait time.Duration, tag string) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o666)
	if err != nil {
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	deadline := time.Now().Add(wait)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			_ = f.Truncate(0)
			_, _ = f.Seek(0, io.SeekStart)
			fmt.Fprintf(f, "pid %d %s %s\n", os.Getpid(), tag, time.Now().Format(time.RFC3339))
			return &Lock{f: f}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			f.Close()
			return nil, fmt.Errorf("flock %s: %w", path, err)
		}
		if time.Now().After(deadline) {
			holder, _ := os.ReadFile(path)
			f.Close()
			return nil, fmt.Errorf("another Quartus flow holds %s (%s); waited %s", path, strings.TrimSpace(string(holder)), wait)
		}
		select {
		case <-ctx.Done():
			f.Close()
			return nil, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// Release drops the lock.
func (l *Lock) Release() {
	if l == nil || l.f == nil {
		return
	}
	_ = syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	l.f.Close()
	l.f = nil
}

// --- memory gate --------------------------------------------------------------

// MemAvailableMB parses MemAvailable (in MB) from a /proc/meminfo-shaped file;
// -1 if it cannot be read.
func MemAvailableMB(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return -1
	}
	return ParseMemAvailableMB(string(data))
}

// ParseMemAvailableMB is the pure part of MemAvailableMB.
func ParseMemAvailableMB(meminfo string) int {
	for _, ln := range strings.Split(meminfo, "\n") {
		if strings.HasPrefix(ln, "MemAvailable:") {
			f := strings.Fields(ln)
			if len(f) < 2 {
				return -1
			}
			kb, err := strconv.Atoi(f[1])
			if err != nil {
				return -1
			}
			return kb / 1024
		}
	}
	return -1
}

// --- the flow -------------------------------------------------------------------

// Run executes the whole flow. It returns an error for anything that stops
// the flow (lock, memory, staging, a tool exiting non-zero, a wrong-size rbf);
// report-level defects come back in Result.Defects().
func Run(ctx context.Context, c Config) (*Result, error) {
	if err := c.fill(); err != nil {
		return nil, err
	}
	design := filepath.Join(c.FPGADir, c.Design)
	qsfPath := filepath.Join(design, c.Design+".qsf")
	qsfText, err := os.ReadFile(qsfPath)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", qsfPath, err)
	}

	lock, err := AcquireLock(ctx, c.LockPath, c.LockWait, "buildfpga "+c.Design)
	if err != nil {
		return nil, err
	}
	defer lock.Release()
	fmt.Fprintf(c.Log, "lock: holding %s\n", c.LockPath)

	if c.MinFreeMB > 0 {
		if m := MemAvailableMB(c.MemInfo); m >= 0 && m < c.MinFreeMB {
			return nil, fmt.Errorf("only %d MB available (< %d MB): refusing to start a Quartus flow", m, c.MinFreeMB)
		} else if m >= 0 {
			fmt.Fprintf(c.Log, "memory: %d MB available (floor %d)\n", m, c.MinFreeMB)
		} else {
			fmt.Fprintf(c.Log, "memory: %s unreadable, gate skipped\n", c.MemInfo)
		}
	}

	sources, err := stage(c, design, string(qsfText))
	if err != nil {
		return nil, err
	}
	res := &Result{Sources: sources}
	fmt.Fprintf(c.Log, "staged %d sources into %s\n", len(sources), c.OutDir)

	stages := [][]string{
		{"quartus_map", c.Design},
		{"quartus_fit", c.Design},
		{"quartus_sta", c.Design},
		{"quartus_asm", c.Design},
		{"quartus_cpf", "-c", "-o", "bitstream_compression=off",
			filepath.Join("output_files", c.Design+".sof"), c.Design + ".rbf"},
	}
	for _, s := range stages {
		st, err := runTool(ctx, c, s[0], s[1:])
		res.Stages = append(res.Stages, st)
		if err != nil {
			return res, err
		}
	}

	res.RBF = filepath.Join(c.OutDir, c.Design+".rbf")
	fi, err := os.Stat(res.RBF)
	if err != nil {
		return res, fmt.Errorf("quartus_cpf produced no rbf: %w", err)
	}
	res.RBFSize = fi.Size()
	if fi.Size() != RBFBytes {
		return res, fmt.Errorf("%s is %d bytes, want %d (compression on, or wrong device?)", res.RBF, fi.Size(), RBFBytes)
	}

	res.FitRpt = filepath.Join(c.OutDir, "output_files", c.Design+".fit.rpt")
	fitText, err := os.ReadFile(res.FitRpt)
	if err != nil {
		return res, fmt.Errorf("reading fit report: %w", err)
	}
	res.Fit = ParseFitReport(string(fitText), QSFPins(string(qsfText)))

	res.STARpt = filepath.Join(c.OutDir, "output_files", c.Design+".sta.rpt")
	staText, err := os.ReadFile(res.STARpt)
	if err != nil {
		return res, fmt.Errorf("reading STA report: %w", err)
	}
	res.Timing = ParseSTAReport(string(staText))
	return res, nil
}

// stage wipes OutDir and copies the design's inputs into it flat. It returns
// the staged .v file names in QSF order after checking the QSF lists exactly
// that set and that the SDC it names is present.
func stage(c Config, design, qsf string) ([]string, error) {
	if err := os.RemoveAll(c.OutDir); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(c.OutDir, 0o755); err != nil {
		return nil, err
	}
	patterns := []string{
		filepath.Join(c.FPGADir, "common", "*.v"),
		filepath.Join(design, "*.v"),
		filepath.Join(design, "*.vh"),
		filepath.Join(design, "*.sdc"),
		filepath.Join(design, "*.qsf"),
	}
	copied := map[string]string{}
	var vfiles []string
	for _, pat := range patterns {
		matches, _ := filepath.Glob(pat)
		sort.Strings(matches)
		for _, src := range matches {
			base := filepath.Base(src)
			if prev, dup := copied[base]; dup {
				return nil, fmt.Errorf("staging: %s and %s both stage as %s", prev, src, base)
			}
			copied[base] = src
			if err := copyFile(src, filepath.Join(c.OutDir, base)); err != nil {
				return nil, err
			}
			if strings.HasSuffix(base, ".v") {
				vfiles = append(vfiles, base)
			}
		}
	}
	if len(vfiles) == 0 {
		return nil, fmt.Errorf("staging: no Verilog sources in %s or %s", filepath.Join(c.FPGADir, "common"), design)
	}

	listed := QSFGlobals(qsf, "VERILOG_FILE")
	have := map[string]bool{}
	for _, v := range vfiles {
		have[v] = true
	}
	var missing, extra []string
	seen := map[string]bool{}
	for _, l := range listed {
		seen[l] = true
		if !have[l] {
			extra = append(extra, l)
		}
	}
	for _, v := range vfiles {
		if !seen[v] {
			missing = append(missing, v)
		}
	}
	if len(missing) > 0 || len(extra) > 0 {
		return nil, fmt.Errorf("%s.qsf VERILOG_FILE list is stale (regenerate with `make -C fpga qsf`): not listed %v, listed but absent %v",
			c.Design, missing, extra)
	}
	sdc := QSFGlobal(qsf, "SDC_FILE")
	if sdc == "" {
		return nil, fmt.Errorf("%s.qsf names no SDC_FILE: timing would be unconstrained", c.Design)
	}
	if _, ok := copied[sdc]; !ok {
		return nil, fmt.Errorf("%s.qsf names SDC_FILE %s but %s does not exist: timing would be unconstrained", c.Design, sdc, filepath.Join(design, sdc))
	}
	if top := QSFGlobal(qsf, "TOP_LEVEL_ENTITY"); top == "" {
		return nil, fmt.Errorf("%s.qsf names no TOP_LEVEL_ENTITY", c.Design)
	}
	qpf := fmt.Sprintf("PROJECT_REVISION = %q\n", c.Design)
	if err := os.WriteFile(filepath.Join(c.OutDir, c.Design+".qpf"), []byte(qpf), 0o644); err != nil {
		return nil, err
	}
	// out/ is build product only (staged copies, db/, output_files/, logs, the
	// rbf): it ignores itself so no build product can ever be tracked.
	if err := os.WriteFile(filepath.Join(c.OutDir, ".gitignore"), []byte("*\n"), 0o644); err != nil {
		return nil, err
	}
	return listed, nil
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}

// runTool runs one quartus_* binary in OutDir with its combined output
// captured into OutDir/<tool>.log.
func runTool(ctx context.Context, c Config, tool string, args []string) (Stage, error) {
	st := Stage{Tool: tool, Args: args, LogFile: filepath.Join(c.OutDir, tool+".log")}
	logf, err := os.Create(st.LogFile)
	if err != nil {
		return st, err
	}
	defer logf.Close()
	cmd := exec.CommandContext(ctx, filepath.Join(c.Root, "bin", tool), args...)
	cmd.Dir = c.OutDir
	cmd.Env = append(os.Environ(),
		"QUARTUS_ROOTDIR="+c.Root,
		"PATH="+filepath.Join(c.Root, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	cmd.Stdout = logf
	cmd.Stderr = logf
	fmt.Fprintf(c.Log, "run: %s %s\n", tool, strings.Join(args, " "))
	t0 := time.Now()
	err = cmd.Run()
	st.Duration = time.Since(t0)
	fmt.Fprintf(c.Log, "     %s done in %s\n", tool, st.Duration.Round(time.Second))
	if err != nil {
		tail, _ := os.ReadFile(st.LogFile)
		return st, fmt.Errorf("%s failed: %w\n--- tail of %s ---\n%s", tool, err, st.LogFile, lastLines(string(tail), 25))
	}
	return st, nil
}

func lastLines(s string, n int) string {
	ls := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(ls) > n {
		ls = ls[len(ls)-n:]
	}
	return strings.Join(ls, "\n")
}
