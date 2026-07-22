package quartus

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestStripComments(t *testing.T) {
	in := "a // line `include \"nope.vh\"\n" +
		"b /* block `include \"also_nope.vh\" */ c\n" +
		"`include \"real.vh\"\n"
	got := stripComments(in)
	if want := "a \nb  c\n`include \"real.vh\"\n"; got != want {
		t.Fatalf("stripComments:\n got %q\nwant %q", got, want)
	}
}

func TestScanIncludes(t *testing.T) {
	// Comment mentions (like regmux.vh's header, which names `include "regs.vh"
	// in prose) must be ignored; real directives kept in first-seen order, deduped.
	src := "// `include this AFTER `include \"regs.vh\" in prose\n" +
		"`include \"regs.vh\"\n" +
		"module m; `include \"regmux.vh\"\n" +
		"`include \"regs.vh\" // dup\n" +
		"endmodule\n"
	got := ScanIncludes(src)
	want := []string{"regs.vh", "regmux.vh"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ScanIncludes = %v, want %v", got, want)
	}
}

func TestScanIncludesNone(t *testing.T) {
	if got := ScanIncludes("module m; endmodule\n"); len(got) != 0 {
		t.Fatalf("ScanIncludes on no-include source = %v, want empty", got)
	}
}

func TestResolveIncludes(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "regs.vh", "`define SEL_X 8'd1\n")
	// regmux.vh nests an include; the resolver must follow it recursively.
	write(t, dir, "regmux.vh", "`include \"inner.vh\"\nwire we_X;\n")
	write(t, dir, "inner.vh", "// leaf\n")

	sources := []Source{{
		Name:    "acq.v",
		Content: "`include \"regs.vh\"\nmodule acq; `include \"regmux.vh\" endmodule\n",
	}}
	incs, err := ResolveIncludes(sources, dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	byName := map[string]string{}
	for _, inc := range incs {
		names = append(names, inc.Name)
		byName[inc.Name] = inc.Content
	}
	want := []string{"regs.vh", "regmux.vh", "inner.vh"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("resolved names = %v, want %v (order = first-seen, recursive)", names, want)
	}
	if byName["regs.vh"] != "`define SEL_X 8'd1\n" {
		t.Fatalf("regs.vh content not copied verbatim: %q", byName["regs.vh"])
	}
}

func TestResolveIncludesMissing(t *testing.T) {
	dir := t.TempDir()
	sources := []Source{{Name: "acq.v", Content: "`include \"gone.vh\"\n"}}
	if _, err := ResolveIncludes(sources, dir); err == nil {
		t.Fatal("expected an error for a missing include, got nil")
	}
}

func TestResolveIncludesSearchOrder(t *testing.T) {
	primary := t.TempDir()
	fallback := t.TempDir()
	write(t, fallback, "regs.vh", "from-fallback\n")
	sources := []Source{{Name: "acq.v", Content: "`include \"regs.vh\"\n"}}
	incs, err := ResolveIncludes(sources, primary, fallback)
	if err != nil {
		t.Fatal(err)
	}
	if len(incs) != 1 || incs[0].Content != "from-fallback\n" {
		t.Fatalf("resolver did not fall through to the second dir: %+v", incs)
	}
}

func TestParseMemAvailableMB(t *testing.T) {
	meminfo := "MemTotal:        3999600 kB\n" +
		"MemFree:          123456 kB\n" +
		"MemAvailable:    2048000 kB\n" +
		"Buffers:           10000 kB\n"
	if got, want := parseMemAvailableMB([]byte(meminfo)), 2000; got != want {
		t.Fatalf("parseMemAvailableMB = %d, want %d", got, want)
	}
	if got := parseMemAvailableMB([]byte("MemTotal: 100 kB\n")); got != -1 {
		t.Fatalf("parseMemAvailableMB without MemAvailable = %d, want -1", got)
	}
	if got := parseMemAvailableMB([]byte("MemAvailable: notanumber kB\n")); got != -1 {
		t.Fatalf("parseMemAvailableMB with bad number = %d, want -1", got)
	}
}

func TestVerilogFilesIn(t *testing.T) {
	qsf := "set_global_assignment -name VERILOG_FILE acq.v\n" +
		"set_global_assignment -name VERILOG_FILE \"spine.v\"\n" +
		"# set_global_assignment -name VERILOG_FILE commented.v\n" +
		"set_global_assignment -name DEVICE EP4CE10F17C8\n"
	got := verilogFilesIn(qsf)
	if !got["acq.v"] || !got["spine.v"] {
		t.Fatalf("verilogFilesIn missing a listed file: %v", got)
	}
	if got["commented.v"] {
		t.Fatal("verilogFilesIn picked up a commented-out VERILOG_FILE")
	}
	if len(got) != 2 {
		t.Fatalf("verilogFilesIn = %v, want exactly {acq.v, spine.v}", got)
	}
}

func TestHasAssignment(t *testing.T) {
	qsf := "set_global_assignment -name TOP_LEVEL_ENTITY acq\n" +
		"# set_global_assignment -name PROJECT_OUTPUT_DIRECTORY output_files\n"
	if !hasAssignment(qsf, "TOP_LEVEL_ENTITY") {
		t.Fatal("hasAssignment should find TOP_LEVEL_ENTITY")
	}
	if hasAssignment(qsf, "PROJECT_OUTPUT_DIRECTORY") {
		t.Fatal("hasAssignment should ignore a commented assignment")
	}
	if hasAssignment(qsf, "DEVICE") {
		t.Fatal("hasAssignment should not find an absent assignment")
	}
}

func TestAssembleQSF_AddsMissing(t *testing.T) {
	base := "set_global_assignment -name FAMILY \"Cyclone IV E\"\n" +
		"set_global_assignment -name DEVICE EP4CE10F17C8\n"
	sources := []Source{{Name: "acq.v"}, {Name: "spine.v"}, {Name: "dac.v"}}
	got := assembleQSF(base, "acq", sources)

	for _, want := range []string{
		"set_global_assignment -name TOP_LEVEL_ENTITY acq",
		"set_global_assignment -name PROJECT_OUTPUT_DIRECTORY output_files",
		"set_global_assignment -name VERILOG_FILE acq.v",
		"set_global_assignment -name VERILOG_FILE spine.v",
		"set_global_assignment -name VERILOG_FILE dac.v",
	} {
		if !contains(got, want) {
			t.Fatalf("assembleQSF missing %q in:\n%s", want, got)
		}
	}
	// The bench base is preserved verbatim.
	if !contains(got, "set_global_assignment -name DEVICE EP4CE10F17C8") {
		t.Fatal("assembleQSF dropped the base device assignment")
	}
}

func TestAssembleQSF_NoDuplicates(t *testing.T) {
	// A base that already lists sources and sets TOP/OUTPUT_DIR must not be
	// re-augmented (no duplicate VERILOG_FILE / TOP_LEVEL_ENTITY lines).
	base := "set_global_assignment -name TOP_LEVEL_ENTITY acq\n" +
		"set_global_assignment -name PROJECT_OUTPUT_DIRECTORY output_files\n" +
		"set_global_assignment -name VERILOG_FILE acq.v\n"
	sources := []Source{{Name: "acq.v"}, {Name: "spine.v"}}
	got := assembleQSF(base, "acq", sources)

	if n := count(got, "set_global_assignment -name VERILOG_FILE acq.v"); n != 1 {
		t.Fatalf("acq.v listed %d times, want 1:\n%s", n, got)
	}
	if n := count(got, "set_global_assignment -name TOP_LEVEL_ENTITY"); n != 1 {
		t.Fatalf("TOP_LEVEL_ENTITY set %d times, want 1", n)
	}
	if !contains(got, "set_global_assignment -name VERILOG_FILE spine.v") {
		t.Fatal("assembleQSF did not add the missing spine.v")
	}
}

func TestAssembleQSF_Deterministic(t *testing.T) {
	base := "set_global_assignment -name DEVICE EP4CE10F17C8\n"
	sources := []Source{{Name: "acq.v"}, {Name: "spine.v"}}
	if a, b := assembleQSF(base, "acq", sources), assembleQSF(base, "acq", sources); a != b {
		t.Fatal("assembleQSF is not deterministic")
	}
}

func TestCompileNoSources(t *testing.T) {
	// Guard path only — never reaches Quartus.
	if _, err := New(t.TempDir()).Compile(Project{Name: "acq"}, filepath.Join(t.TempDir(), "acq.rbf")); err == nil {
		t.Fatal("Compile with no sources should error before invoking Quartus")
	}
}

func TestDefaultRootEnvOverride(t *testing.T) {
	t.Setenv("QUARTUS_ROOTDIR", "/opt/quartus-custom")
	if got := DefaultRoot(); got != "/opt/quartus-custom" {
		t.Fatalf("DefaultRoot = %q, want the env override", got)
	}
}

func TestLastLines(t *testing.T) {
	if got := lastLines("a\nb\nc\nd\n", 2); got != "c\nd" {
		t.Fatalf("lastLines = %q, want \"c\\nd\"", got)
	}
	if got := lastLines("only\n", 5); got != "only" {
		t.Fatalf("lastLines = %q, want \"only\"", got)
	}
}

// --- helpers ---------------------------------------------------------------

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func contains(hay, needle string) bool { return strings.Contains(hay, needle) }

func count(hay, needle string) int { return strings.Count(hay, needle) }
