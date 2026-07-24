// Package quartus drives Intel/Altera Quartus Prime headlessly to compile a
// multi-file Verilog project into a Cyclone IV E .rbf. It is pure Go, stdlib
// only, and memory-gated so the small dev box never runs two flows at once.
//
// It is the fpga module's Phase-B build driver. cmd/buildacq hands it the
// standard design's sources (acq.v + adcif/spine/capture/envelope/drain.v), the
// bench-supplied acq.qsf (device / pins / IO), and the generated regs.vh /
// regmux.vh includes; Compile writes them all into a scratch work dir, runs
// map -> fit -> asm -> cpf, and returns the path to a bitstream that must be
// exactly RBFBytes.
//
// This is an improvement over the proving-ground driver, whose Compile took a
// single Verilog string and wrote one <name>.v. The modular owned design
// (§2 of standard/docs/DESIGN.md) is N source files, so a Project carries a
// slice of sources; the driver writes each one and lists it as a VERILOG_FILE
// in the assembled QSF (it still copies the `include-d headers next to them).
package quartus

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// RBFBytes is the fixed uncompressed size of a valid EP4CE10F17C8 bitstream.
// A cpf output of any other size means the fit changed (M9K not preserved, a
// different device, or compression left on) and must fail the build.
const RBFBytes = 368011

// DefaultRoot is the pinned Quartus 21.1 Lite install ($QUARTUS_ROOTDIR overrides).
func DefaultRoot() string {
	if r := os.Getenv("QUARTUS_ROOTDIR"); r != "" {
		return r
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "intelFPGA_lite", "21.1", "quartus")
}

// Source is one Verilog source file (<Name> like "acq.v") of a multi-file
// project. Every Source is listed as a VERILOG_FILE in the assembled QSF.
type Source struct{ Name, Content string }

// IncludeFile is a file referenced by a design's `include "Name"` (e.g. the
// generated regs.vh / regmux.vh). It is copied next to the sources so the
// include resolves, but it is NOT listed as a VERILOG_FILE.
type IncludeFile struct{ Name, Content string }

// Project is a multi-file Verilog design compiled into one .rbf.
type Project struct {
	Name string // project + revision name (e.g. "acq"); output files use it
	Top  string // top-level entity (defaults to Name if empty)
	// QSF is the bench-supplied device / pins / IO assignments. The driver
	// appends a VERILOG_FILE line for every Source the QSF omits, plus
	// TOP_LEVEL_ENTITY / PROJECT_OUTPUT_DIRECTORY if absent.
	QSF      string
	Sources  []Source      // the .v files (at least one)
	Includes []IncludeFile // the `include-d headers (regs.vh, regmux.vh, ...)
}

// --- include scanning / resolution (pure; testable without Quartus) ---------

var includeRE = regexp.MustCompile("`include\\s+\"([^\"]+)\"")

// stripComments removes // line comments and /* */ block comments from Verilog
// so an `include mentioned in prose (regmux.vh's header names `include "regs.vh"
// in a comment) is not mistaken for a real directive. It does not model string
// literals — the generated headers contain none, which is all this build tool
// scans.
func stripComments(src string) string {
	var b strings.Builder
	b.Grow(len(src))
	inLine, inBlock := false, false
	for i := 0; i < len(src); i++ {
		switch {
		case inLine:
			if src[i] == '\n' {
				inLine = false
				b.WriteByte('\n')
			}
		case inBlock:
			if src[i] == '*' && i+1 < len(src) && src[i+1] == '/' {
				inBlock = false
				i++
			}
		case src[i] == '/' && i+1 < len(src) && src[i+1] == '/':
			inLine = true
			i++
		case src[i] == '/' && i+1 < len(src) && src[i+1] == '*':
			inBlock = true
			i++
		default:
			b.WriteByte(src[i])
		}
	}
	return b.String()
}

// ScanIncludes returns the `include "x" file names in src, in first-seen order,
// deduplicated, ignoring names that appear only inside comments.
func ScanIncludes(src string) []string {
	var names []string
	seen := map[string]bool{}
	for _, m := range includeRE.FindAllStringSubmatch(stripComments(src), -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			names = append(names, m[1])
		}
	}
	return names
}

// ResolveIncludes scans every source (and, recursively, each header it pulls in)
// for `include directives and reads each named file from the first of dirs that
// holds it — so a design's generated regs.vh / regmux.vh get copied into the
// work dir and the include compiles. It errors if any include cannot be found.
func ResolveIncludes(sources []Source, dirs ...string) ([]IncludeFile, error) {
	var queue []string
	for _, s := range sources {
		queue = append(queue, ScanIncludes(s.Content)...)
	}
	seen := map[string]bool{}
	var incs []IncludeFile
	for i := 0; i < len(queue); i++ {
		name := queue[i]
		if seen[name] {
			continue
		}
		seen[name] = true
		data, err := readFrom(name, dirs)
		if err != nil {
			return nil, err
		}
		incs = append(incs, IncludeFile{Name: name, Content: data})
		queue = append(queue, ScanIncludes(data)...) // nested includes
	}
	return incs, nil
}

func readFrom(name string, dirs []string) (string, error) {
	for _, d := range dirs {
		if data, err := os.ReadFile(filepath.Join(d, name)); err == nil {
			return string(data), nil
		}
	}
	return "", fmt.Errorf("`include %q not found in %s", name, strings.Join(dirs, " or "))
}

// --- QSF assembly (pure; testable without Quartus) --------------------------

// assembleQSF returns the QSF to write for a multi-file project: the bench base
// QSF (device / pins / IO) plus a VERILOG_FILE line for every source it does not
// already list, plus TOP_LEVEL_ENTITY / PROJECT_OUTPUT_DIRECTORY if absent. This
// is the multi-file generalization: the base QSF need not be kept in sync with
// the source-file set by hand, and re-listing a file it already names is avoided.
func assembleQSF(base, top string, sources []Source) string {
	var b strings.Builder
	b.WriteString(base)
	if len(base) > 0 && !strings.HasSuffix(base, "\n") {
		b.WriteByte('\n')
	}
	b.WriteString("\n# --- appended by the quartus driver (multi-file project) ---\n")
	if !hasAssignment(base, "TOP_LEVEL_ENTITY") {
		fmt.Fprintf(&b, "set_global_assignment -name TOP_LEVEL_ENTITY %s\n", top)
	}
	if !hasAssignment(base, "PROJECT_OUTPUT_DIRECTORY") {
		b.WriteString("set_global_assignment -name PROJECT_OUTPUT_DIRECTORY output_files\n")
	}
	listed := verilogFilesIn(base)
	for _, s := range sources {
		if !listed[s.Name] {
			fmt.Fprintf(&b, "set_global_assignment -name VERILOG_FILE %s\n", s.Name)
		}
	}
	return b.String()
}

// hasAssignment reports whether the QSF sets a global assignment of the given
// name on a non-comment line.
func hasAssignment(qsf, name string) bool {
	needle := "-name " + name + " "
	for _, ln := range strings.Split(qsf, "\n") {
		ln = strings.TrimSpace(ln)
		if strings.HasPrefix(ln, "#") {
			continue
		}
		if strings.Contains(ln, needle) {
			return true
		}
	}
	return false
}

// verilogFilesIn returns the set of file names already listed as VERILOG_FILE in
// the QSF (quotes and any trailing per-file options stripped).
func verilogFilesIn(qsf string) map[string]bool {
	const key = "-name VERILOG_FILE "
	out := map[string]bool{}
	for _, ln := range strings.Split(qsf, "\n") {
		ln = strings.TrimSpace(ln)
		if strings.HasPrefix(ln, "#") {
			continue
		}
		i := strings.Index(ln, key)
		if i < 0 {
			continue
		}
		rest := strings.Fields(ln[i+len(key):])
		if len(rest) == 0 {
			continue
		}
		if name := strings.Trim(rest[0], "\""); name != "" {
			out[name] = true
		}
	}
	return out
}

// --- memory gate ------------------------------------------------------------

// MemAvailableMB reads /proc/meminfo MemAvailable in MB (-1 if unknown).
func MemAvailableMB() int {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return -1
	}
	return parseMemAvailableMB(data)
}

func parseMemAvailableMB(data []byte) int {
	for _, ln := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(ln, "MemAvailable:") {
			if f := strings.Fields(ln); len(f) >= 2 {
				kb, err := strconv.Atoi(f[1])
				if err != nil {
					return -1
				}
				return kb / 1024
			}
		}
	}
	return -1
}

// Running returns any live quartus_* processes (via pgrep), or nil.
func Running() []string {
	out, err := exec.Command("pgrep", "-af", "quartus_").Output()
	if err != nil {
		return nil
	}
	var ps []string
	for _, ln := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if ln != "" && !strings.Contains(ln, "pgrep") {
			ps = append(ps, ln)
		}
	}
	return ps
}

// GateReady returns an error unless NO Quartus is running and free RAM >= minMB.
// The 3.8 GiB dev box OOMs on two concurrent flows — never bypass this.
func GateReady(minMB int) error {
	if ps := Running(); len(ps) > 0 {
		return fmt.Errorf("a Quartus flow is already running (refusing a second): %s", strings.Join(ps, "; "))
	}
	if m := MemAvailableMB(); m >= 0 && m < minMB {
		return fmt.Errorf("only %d MB free (< %d) — refusing to start a Quartus flow", m, minMB)
	}
	return nil
}

// --- the compiler ------------------------------------------------------------

// Compiler drives the headless flow.
type Compiler struct {
	Root string // Quartus install (bin/ underneath)
	Work string // scratch root (one subdir per design; wiped each side)
}

// New returns a Compiler using DefaultRoot() and the given work-dir root.
func New(work string) Compiler { return Compiler{Root: DefaultRoot(), Work: work} }

func (c Compiler) tool(name string) string { return filepath.Join(c.Root, "bin", name) }

func (c Compiler) run(dir, tool string, args ...string) error {
	cmd := exec.Command(c.tool(tool), args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"QUARTUS_ROOTDIR="+c.Root,
		"PATH="+filepath.Join(c.Root, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s failed: %w\n%s", tool, err, lastLines(string(out), 20))
	}
	return nil
}

// Compile writes the project's sources, the `include-d headers, an assembled QSF
// and a QPF into Work/<Name>/, runs map -> fit -> asm -> cpf, and returns the
// absolute path to outRBF if it is exactly RBFBytes. The work subdir is wiped on
// both sides. Gate the flow with GateReady before calling — Compile does not.
func (c Compiler) Compile(p Project, outRBF string) (string, error) {
	if len(p.Sources) == 0 {
		return "", fmt.Errorf("project %q has no Verilog sources", p.Name)
	}
	top := p.Top
	if top == "" {
		top = p.Name
	}
	outRBF, _ = filepath.Abs(outRBF)
	proj := filepath.Join(c.Work, p.Name)
	os.RemoveAll(proj)
	if err := os.MkdirAll(proj, 0o755); err != nil {
		return "", err
	}
	defer os.RemoveAll(proj)

	type fileWrite struct{ name, body string }
	writes := []fileWrite{
		{p.Name + ".qsf", assembleQSF(p.QSF, top, p.Sources)},
		{p.Name + ".qpf", fmt.Sprintf("PROJECT_REVISION = %q\n", p.Name)},
	}
	for _, s := range p.Sources {
		writes = append(writes, fileWrite{s.Name, s.Content})
	}
	for _, inc := range p.Includes {
		writes = append(writes, fileWrite{inc.Name, inc.Content})
	}
	for _, w := range writes {
		if err := os.WriteFile(filepath.Join(proj, w.name), []byte(w.body), 0o644); err != nil {
			return "", err
		}
	}

	for _, t := range []string{"quartus_map", "quartus_fit", "quartus_asm"} {
		if err := c.run(proj, t, p.Name); err != nil {
			return "", err
		}
	}
	sof := filepath.Join(proj, "output_files", p.Name+".sof")
	if _, err := os.Stat(sof); err != nil {
		sof = filepath.Join(proj, p.Name+".sof")
	}
	if err := os.MkdirAll(filepath.Dir(outRBF), 0o755); err != nil {
		return "", err
	}
	if err := c.run(proj, "quartus_cpf", "-c", "-o", "bitstream_compression=off", sof, outRBF); err != nil {
		return "", err
	}
	fi, err := os.Stat(outRBF)
	if err != nil {
		return "", fmt.Errorf("cpf produced no rbf: %w", err)
	}
	if fi.Size() != RBFBytes {
		return "", fmt.Errorf("rbf is %d bytes, want %d", fi.Size(), RBFBytes)
	}
	return outRBF, nil
}

func lastLines(s string, n int) string {
	ls := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(ls) > n {
		ls = ls[len(ls)-n:]
	}
	return strings.Join(ls, "\n")
}
