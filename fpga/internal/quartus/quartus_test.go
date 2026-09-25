// ENGMODEL-OWNER-UNIT: FU-FPGA-QUARTUS
package quartus

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// --- fixtures: report texts in the exact shape Quartus 21.1 writes ----------------

const fitOK = `Fitter report for default
Wed Sep  4 22:00:00 2026
Quartus Prime Version 21.1.0 Build 842 10/21/2021 SJ Lite Edition

+-----------------------------------------------------------------------------------+
; Fitter Summary                                                                    ;
+------------------------------------+----------------------------------------------+
; Fitter Status                      ; Successful - Wed Sep  4 22:00:00 2026        ;
; Revision Name                      ; default                                      ;
; Top-level Entity Name              ; default_top                                  ;
; Family                             ; Cyclone IV E                                 ;
; Device                             ; EP4CE10F17C8                                 ;
; Total logic elements               ; 4,321 / 10,320 ( 42 % )                      ;
;     Total combinational functions  ; 3,000 / 10,320 ( 29 % )                      ;
;     Dedicated logic registers      ; 2,500 / 10,320 ( 24 % )                      ;
; Total registers                    ; 2500                                         ;
; Total pins                         ; 162 / 180 ( 90 % )                           ;
; Total virtual pins                 ; 0                                            ;
; Total memory bits                  ; 368,640 / 423,936 ( 87 % )                   ;
; Embedded Multiplier 9-bit elements ; 0 / 46 ( 0 % )                               ;
; Total PLLs                         ; 2 / 2 ( 100 % )                              ;
+------------------------------------+----------------------------------------------+


+---------------------------------------------------------------------------+
; Fitter Resource Usage Summary                                             ;
+---------------------------------------------+-----------------------------+
; Resource                                    ; Usage                       ;
+---------------------------------------------+-----------------------------+
; Total logic elements                        ; 4,321 / 10,320 ( 42 % )     ;
; M9Ks                                        ; 44 / 46 ( 96 % )            ;
; Total block memory bits                     ; 368,640 / 423,936 ( 87 % )  ;
+---------------------------------------------+-----------------------------+
%%IOWARN%%

+-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------+
; All Package Pins                                                                                                                                                                        ;
+----------+------------+----------+-----------------------------------------------------------+--------+--------------+---------+------------+-----------------+----------+--------------+
; Location ; Pad Number ; I/O Bank ; Pin Name/Usage                                            ; Dir.   ; I/O Standard ; Voltage ; I/O Type   ; User Assignment ; Bus Hold ; Weak Pull Up ;
+----------+------------+----------+-----------------------------------------------------------+--------+--------------+---------+------------+-----------------+----------+--------------+
; A1       ;            ; 8        ; VCCIO8                                                    ; power  ;              ; 3.3V    ; --         ;                 ; --       ; --           ;
; A2       ; 194        ; 8        ; RESERVED_INPUT                                            ;        ;              ;         ; Column I/O ;                 ; no       ; Off          ;
; A10      ; 168        ; 7        ; gpmc_d[0]                                                 ; bidir  ; 3.3-V LVTTL  ;         ; Column I/O ; Y               ; no       ; Off          ;
; C2       ; 4          ; 1        ; clk                                                       ; input  ; 3.3-V LVTTL  ;         ; Row I/O    ; Y               ; no       ; Off          ;
; D2       ; 7          ; 1        ; %%D2USAGE%%                                               ; output ; 3.3-V LVTTL  ;         ; Row I/O    ; Y               ; no       ; Off          ;
; H1       ; 15         ; 1        ; ~ALTERA_DCLK~                                             ; output ; 3.3-V LVTTL  ;         ; Row I/O    ; N               ; no       ; On           ;
+----------+------------+----------+-----------------------------------------------------------+--------+--------------+---------+------------+-----------------+----------+--------------+


+-----------------+
; Fitter Messages ;
+-----------------+
Info (11000): Started Fitter
Warning (169177): 80 pins must meet Intel FPGA requirements for 3.3-, 3.0-, and 2.5-V interfaces. For more information, refer to AN 447.
%%MSGS%%
Info: Quartus Prime Fitter was successful. 0 errors, 1 warning
`

const ioWarnTable = `
+--------------------------------------------+
; I/O Assignment Warnings                    ;
+--------------+-----------------------------+
; Pin Name     ; Reason                      ;
+--------------+-----------------------------+
; lane[3]      ; Missing location assignment ;
+--------------+-----------------------------+
`

// TRLC-LINKS: REQ-SDS-154
func fitReport(variant string) string {
	s := fitOK
	switch variant {
	case "ok":
		s = strings.ReplaceAll(s, "%%IOWARN%%", "")
		s = strings.ReplaceAll(s, "%%D2USAGE%%", "~ALTERA_FLASH_nCE_nCSO~ / d2")
		s = strings.ReplaceAll(s, "%%MSGS%%", "")
	case "unassigned":
		s = strings.ReplaceAll(s, "%%IOWARN%%", ioWarnTable)
		s = strings.ReplaceAll(s, "%%D2USAGE%%", "~ALTERA_FLASH_nCE_nCSO~ / d2")
		s = strings.ReplaceAll(s, "%%MSGS%%", "Critical Warning (169085): No exact pin location assignment(s) for 1 pins of 162 total pins. For the list of pins please refer to the I/O Assignment Warnings table in the fitter report.")
	case "unused":
		s = strings.ReplaceAll(s, "%%IOWARN%%", "")
		s = strings.ReplaceAll(s, "%%D2USAGE%%", "~ALTERA_FLASH_nCE_nCSO~ / RESERVED_INPUT")
		s = strings.ReplaceAll(s, "%%MSGS%%", "Warning (15705): Ignored locations or region assignments to the following nodes\n"+
			"    Warning (15706): Node \"d2\" is assigned to location or region, but does not exist in design (or could not be automatically promoted to design names)")
	default:
		panic("fit variant " + variant)
	}
	return s
}

const staOK = `Timing Analyzer report for default

+-------------------------------------+
; Slow 1200mV 85C Model Setup Summary ;
+------------+-------+----------------+
; Clock      ; Slack ; End Point TNS  ;
+------------+-------+----------------+
; clk        ; 2.972 ; 0.000          ;
; pll|clk[0] ; 0.412 ; 0.000          ;
+------------+-------+----------------+


+------------------------------------+
; Slow 1200mV 85C Model Hold Summary ;
+------------+-------+---------------+
; Clock      ; Slack ; End Point TNS ;
+------------+-------+---------------+
; clk        ; 0.311 ; 0.000         ;
; pll|clk[0] ; 0.222 ; 0.000         ;
+------------+-------+---------------+


+------------------------------------+
; Slow 1200mV 0C Model Setup Summary ;
+------------+--------+--------------+
; Clock      ; Slack  ; End Point TNS;
+------------+--------+--------------+
; clk        ; 3.100  ; 0.000        ;
; pll|clk[0] ; %%PLL%% ; 0.000        ;
+------------+--------+--------------+


+------------------------------------+
; Fast 1200mV 0C Model Setup Summary ;
+------------+-------+---------------+
; Clock      ; Slack ; End Point TNS ;
+------------+-------+---------------+
; clk        ; 4.000 ; 0.000         ;
; pll|clk[0] ; 1.500 ; 0.000         ;
+------------+-------+---------------+
`

// staPadded is the shape Quartus writes when a clock name is wider than the
// title: the title cell is padded to the table width (the acq2 default image's
// "u_pll|u_pll_a|auto_generated|pll1|clk[4]" does this; fit iteration 16).
const staPadded = `Timing Analyzer report for default

+-------------------------------------------------------------------+
; Slow 1200mV 85C Model Setup Summary                               ;
+------------------------------------------+--------+---------------+
; Clock                                    ; Slack  ; End Point TNS ;
+------------------------------------------+--------+---------------+
; u_pll|u_pll_a|auto_generated|pll1|clk[4] ; 0.194  ; 0.000         ;
; clk                                      ; 1.129  ; 0.000         ;
+------------------------------------------+--------+---------------+


+-------------------------------------------------------------------+
; Slow 1200mV 85C Model Hold Summary                                ;
+------------------------------------------+--------+---------------+
; Clock                                    ; Slack  ; End Point TNS ;
+------------------------------------------+--------+---------------+
; clk                                      ; 0.432  ; 0.000         ;
+------------------------------------------+--------+---------------+


+-------------------------------------------------------------------+
; Fast 1200mV 0C Model Setup Summary                                ;
+------------------------------------------+--------+---------------+
; Clock                                    ; Slack  ; End Point TNS ;
+------------------------------------------+--------+---------------+
; u_pll|u_pll_a|auto_generated|pll1|clk[4] ; 2.887  ; 0.000         ;
; clk                                      ; 4.662  ; 0.000         ;
+------------------------------------------+--------+---------------+
`

// TRLC-LINKS: REQ-SDS-154
func staReport(variant string) string {
	switch variant {
	case "padded":
		return staPadded
	case "ok":
		return strings.ReplaceAll(staOK, "%%PLL%%", "0.350")
	case "neg":
		return strings.ReplaceAll(staOK, "%%PLL%%", "-0.123")
	case "none":
		return "Timing Analyzer report for default\n\nInfo: no clocks\n"
	}
	panic("sta variant " + variant)
}

const testQSF = `# test qsf
set_global_assignment -name FAMILY "Cyclone IV E"
set_global_assignment -name DEVICE EP4CE10F17C8
set_global_assignment -name TOP_LEVEL_ENTITY default_top
set_global_assignment -name SDC_FILE default.sdc
set_global_assignment -name VERILOG_FILE sync.v
set_global_assignment -name VERILOG_FILE default.v
# set_location_assignment PIN_Z9 -to commented_out
set_location_assignment PIN_A10 -to gpmc_d[0]
set_instance_assignment -name IO_STANDARD "3.3-V LVTTL" -to gpmc_d[0]
set_location_assignment PIN_C2 -to clk
set_location_assignment PIN_D2 -to d2
`

// --- the fake tool chain -------------------------------------------------------------

// fakeQuartus builds <dir>/bin/quartus_{map,fit,sta,asm,cpf} shell scripts.
// Behaviour is steered through the environment (inherited by the tools):
//
//	FAKE_FAIL_TOOL   name of the tool that exits 1
//	FAKE_RBF_BYTES   size of the rbf quartus_cpf writes (default RBFBytes)
//	FAKE_FIT         fit report variant (ok | unassigned | unused)
//	FAKE_STA         sta report variant (ok | neg | none)
//
// Every call appends "<tool> <args>" to <dir>/calls.log and its cwd to <dir>/cwd.log.
// TRLC-LINKS: REQ-SDS-152
func fakeQuartus(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"ok", "unassigned", "unused"} {
		if err := os.WriteFile(filepath.Join(dir, "fit_"+v+".rpt"), []byte(fitReport(v)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, v := range []string{"ok", "neg", "none"} {
		if err := os.WriteFile(filepath.Join(dir, "sta_"+v+".rpt"), []byte(staReport(v)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	common := `#!/bin/sh
FAKE=` + dir + `
tool=$(basename "$0")
echo "$tool $*" >> "$FAKE/calls.log"
pwd >> "$FAKE/cwd.log"
echo "Info: fake $tool running"
if [ "$FAKE_FAIL_TOOL" = "$tool" ]; then echo "Error: fake $tool failure"; exit 1; fi
mkdir -p output_files
`
	scripts := map[string]string{
		"quartus_map": common + `touch output_files/$1.map.rpt
`,
		"quartus_fit": common + `cp "$FAKE/fit_${FAKE_FIT:-ok}.rpt" output_files/$1.fit.rpt
`,
		"quartus_sta": common + `cp "$FAKE/sta_${FAKE_STA:-ok}.rpt" output_files/$1.sta.rpt
`,
		"quartus_asm": common + `echo sof > output_files/$1.sof
`,
		"quartus_cpf": common + `# args: -c -o bitstream_compression=off <sof> <rbf>
sof=$4; rbf=$5
[ -f "$sof" ] || { echo "Error: no sof $sof"; exit 1; }
head -c "${FAKE_RBF_BYTES:-368011}" /dev/zero > "$rbf"
`,
	}
	for name, body := range scripts {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// fakeDesign builds <fpga>/common/sync.v, <fpga>/default/{default.v,regs.vh,default.sdc,default.qsf}.
// TRLC-LINKS: REQ-SDS-153
func fakeDesign(t *testing.T) string {
	t.Helper()
	fpga := t.TempDir()
	files := map[string]string{
		"common/sync.v":       "module sync; endmodule\n",
		"common/sim/tb.v":     "module tb; endmodule\n", // must NOT be staged
		"default/default.v":   "`include \"regs.vh\"\nmodule default_top; endmodule\n",
		"default/regs.vh":     "`define SEL_X 1\n",
		"default/default.sdc": "create_clock -period 12.5 [get_ports clk]\n",
		"default/default.qsf": testQSF,
	}
	for rel, body := range files {
		p := filepath.Join(fpga, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return fpga
}

// TRLC-LINKS: REQ-SDS-152
func meminfo(t *testing.T, availMB int) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "meminfo")
	body := "MemTotal:       49231872 kB\nMemFree:         1600000 kB\nMemAvailable:   " +
		strconv.Itoa(availMB*1024) + " kB\nBuffers:          100000 kB\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// TRLC-LINKS: REQ-SDS-152
func config(t *testing.T, root, fpga string) Config {
	t.Helper()
	return Config{
		Root:      root,
		FPGADir:   fpga,
		Design:    "default",
		LockPath:  filepath.Join(t.TempDir(), "quartus.lock"),
		MinFreeMB: DefaultMinFreeMB,
		MemInfo:   meminfo(t, 20000),
	}
}

// TRLC-LINKS: REQ-SDS-152
func calls(t *testing.T, root string) []string {
	t.Helper()
	data, _ := os.ReadFile(filepath.Join(root, "calls.log"))
	s := strings.TrimSpace(string(data))
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// --- pure parsers -----------------------------------------------------------------

// TRLC-LINKS: REQ-SDS-152
func TestParseMemAvailableMB(t *testing.T) {
	if got := ParseMemAvailableMB("MemTotal: 10 kB\nMemAvailable:   20669440 kB\n"); got != 20185 {
		t.Fatalf("got %d, want 20185", got)
	}
	if got := ParseMemAvailableMB("MemTotal: 10 kB\n"); got != -1 {
		t.Fatalf("missing field: got %d, want -1", got)
	}
	if got := ParseMemAvailableMB("MemAvailable: x kB\n"); got != -1 {
		t.Fatalf("bad number: got %d, want -1", got)
	}
	if got := MemAvailableMB("/nonexistent/meminfo"); got != -1 {
		t.Fatalf("unreadable: got %d, want -1", got)
	}
}

// TRLC-LINKS: REQ-SDS-153
func TestQSFPinsAndGlobals(t *testing.T) {
	pins := QSFPins(testQSF)
	want := map[string]string{"A10": "gpmc_d[0]", "C2": "clk", "D2": "d2"}
	if len(pins) != len(want) {
		t.Fatalf("pins = %v, want %v", pins, want)
	}
	for b, p := range want {
		if pins[b] != p {
			t.Fatalf("pins[%s] = %q, want %q", b, pins[b], p)
		}
	}
	if got := QSFGlobal(testQSF, "TOP_LEVEL_ENTITY"); got != "default_top" {
		t.Fatalf("TOP_LEVEL_ENTITY = %q", got)
	}
	if got := QSFGlobal(testQSF, "FAMILY"); got != "Cyclone IV E" {
		t.Fatalf("FAMILY = %q (quotes must be stripped)", got)
	}
	if got := QSFGlobal(testQSF, "NOPE"); got != "" {
		t.Fatalf("absent global = %q", got)
	}
	if got := QSFGlobals(testQSF, "VERILOG_FILE"); strings.Join(got, ",") != "sync.v,default.v" {
		t.Fatalf("VERILOG_FILE = %v", got)
	}
}

// TRLC-LINKS: REQ-SDS-154
func TestParseFitReportOK(t *testing.T) {
	r := ParseFitReport(fitReport("ok"), QSFPins(testQSF))
	if len(r.Problems) != 0 {
		t.Fatalf("problems on a clean report: %v", r.Problems)
	}
	if r.Summary["Total logic elements"] != "4,321 / 10,320 ( 42 % )" {
		t.Fatalf("summary = %v", r.Summary)
	}
	if r.Summary["M9Ks"] != "44 / 46 ( 96 % )" {
		t.Fatalf("M9Ks = %q", r.Summary["M9Ks"])
	}
	if r.Summary["Total PLLs"] != "2 / 2 ( 100 % )" || r.Summary["Total pins"] != "162 / 180 ( 90 % )" {
		t.Fatalf("summary = %v", r.Summary)
	}
	if r.PinUsage["A2"] != "RESERVED_INPUT" || r.PinUsage["D2"] != "~ALTERA_FLASH_nCE_nCSO~ / d2" {
		t.Fatalf("pin usage = %v", r.PinUsage)
	}
	if len(r.Warnings) != 1 || !strings.Contains(r.Warnings[0], "169177") {
		t.Fatalf("warnings = %v", r.Warnings)
	}
}

// TRLC-LINKS: REQ-SDS-154
func TestParseFitReportUnassigned(t *testing.T) {
	r := ParseFitReport(fitReport("unassigned"), QSFPins(testQSF))
	var kinds []string
	for _, p := range r.Problems {
		kinds = append(kinds, p.Kind+":"+p.Port)
	}
	if strings.Join(kinds, " ") != "unassigned:lane[3]" {
		t.Fatalf("problems = %v", r.Problems)
	}
	if !strings.Contains(r.Problems[0].Detail, "Missing location assignment") {
		t.Fatalf("detail = %q", r.Problems[0].Detail)
	}
}

// TRLC-LINKS: REQ-SDS-154
func TestParseFitReportUnused(t *testing.T) {
	r := ParseFitReport(fitReport("unused"), QSFPins(testQSF))
	if len(r.Problems) != 2 {
		t.Fatalf("want 2 problems (table cross-check + 15706 message), got %v", r.Problems)
	}
	if r.Problems[0].Kind != "unused" || r.Problems[0].Ball != "D2" || r.Problems[0].Port != "d2" {
		t.Fatalf("problem[0] = %+v", r.Problems[0])
	}
	if r.Problems[1].Kind != "unused" || r.Problems[1].Port != "d2" || !strings.Contains(r.Problems[1].Detail, "does not exist") {
		t.Fatalf("problem[1] = %+v", r.Problems[1])
	}
}

// TRLC-LINKS: REQ-SDS-154
func TestParseFitReportBallMissingFromTable(t *testing.T) {
	pins := map[string]string{"A10": "gpmc_d[0]", "T9": "lane[13]"}
	r := ParseFitReport(fitReport("ok"), pins)
	if len(r.Problems) != 1 || r.Problems[0].Ball != "T9" || r.Problems[0].Kind != "unused" {
		t.Fatalf("problems = %v", r.Problems)
	}
}

// TRLC-LINKS: REQ-SDS-154
func TestParseFitReportNoPinTable(t *testing.T) {
	r := ParseFitReport("Fitter report\nInfo: nothing here\n", QSFPins(testQSF))
	if len(r.Problems) != 1 || r.Problems[0].Kind != "message" {
		t.Fatalf("a report without All Package Pins must be flagged, got %v", r.Problems)
	}
}

// TRLC-LINKS: REQ-SDS-154
func TestParseSTAReport(t *testing.T) {
	tr := ParseSTAReport(staReport("ok"))
	if tr.Tables != 3 {
		t.Fatalf("tables = %d, want 3 (hold tables must not count)", tr.Tables)
	}
	if got := tr.Clocks(); strings.Join(got, ",") != "clk,pll|clk[0]" {
		t.Fatalf("clocks = %v", got)
	}
	if tr.WorstSetup["clk"] != 2.972 || tr.Corner["clk"] != "Slow 1200mV 85C" {
		t.Fatalf("clk: %v %v", tr.WorstSetup["clk"], tr.Corner["clk"])
	}
	if tr.WorstSetup["pll|clk[0]"] != 0.350 || tr.Corner["pll|clk[0]"] != "Slow 1200mV 0C" {
		t.Fatalf("pll: %v %v", tr.WorstSetup["pll|clk[0]"], tr.Corner["pll|clk[0]"])
	}
	if f := tr.Failing(); len(f) != 0 {
		t.Fatalf("failing = %v", f)
	}
	neg := ParseSTAReport(staReport("neg"))
	if f := neg.Failing(); strings.Join(f, ",") != "pll|clk[0]" || neg.WorstSetup["pll|clk[0]"] != -0.123 {
		t.Fatalf("neg: failing=%v worst=%v", f, neg.WorstSetup)
	}
	none := ParseSTAReport(staReport("none"))
	if none.Tables != 0 || len(none.WorstSetup) != 0 {
		t.Fatalf("none: %+v", none)
	}
	// padded title cells (wide clock names) are still Setup Summary tables
	pad := ParseSTAReport(staReport("padded"))
	if pad.Tables != 2 || pad.WorstSetup["u_pll|u_pll_a|auto_generated|pll1|clk[4]"] != 0.194 ||
		pad.Corner["u_pll|u_pll_a|auto_generated|pll1|clk[4]"] != "Slow 1200mV 85C" || pad.WorstSetup["clk"] != 1.129 {
		t.Fatalf("padded: %+v", pad)
	}
	if f := pad.Failing(); len(f) != 0 {
		t.Fatalf("padded failing = %v", f)
	}
}

// TestRealDefaultSTAReport parses the acq2 default image's own STA report when
// a build is present (fpga/default/out is a build product, not tracked): all
// three corners must be found and the design must close.
// TRLC-LINKS: REQ-SDS-154
func TestRealDefaultSTAReport(t *testing.T) {
	sta, err := os.ReadFile(filepath.Join("..", "..", "default", "out", "output_files", "default.sta.rpt"))
	if err != nil {
		t.Skip("no default build present:", err)
	}
	tr := ParseSTAReport(string(sta))
	if tr.Tables != 3 {
		t.Fatalf("default.sta.rpt: %d Setup Summary tables, want 3 (padded titles?)", tr.Tables)
	}
	if f := tr.Failing(); len(f) != 0 {
		t.Fatalf("default.sta.rpt: failing clocks %v", f)
	}
	if _, ok := tr.WorstSetup["clk"]; !ok {
		t.Fatalf("default.sta.rpt: no clk row: %v", tr.Clocks())
	}
}

// --- the flow against the fake tools ---------------------------------------------------

// TRLC-LINKS: REQ-SDS-152, REQ-SDS-153, REQ-SDS-154
func TestRunHappyPath(t *testing.T) {
	root := fakeQuartus(t)
	fpga := fakeDesign(t)
	c := config(t, root, fpga)
	var log strings.Builder
	c.Log = &log

	res, err := Run(context.Background(), c)
	if err != nil {
		t.Fatalf("Run: %v\nlog:\n%s", err, log.String())
	}
	wantCalls := []string{
		"quartus_map default",
		"quartus_fit default",
		"quartus_sta default",
		"quartus_asm default",
		"quartus_cpf -c -o bitstream_compression=off output_files/default.sof default.rbf",
	}
	if got := calls(t, root); strings.Join(got, "\n") != strings.Join(wantCalls, "\n") {
		t.Fatalf("tool calls:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(wantCalls, "\n"))
	}
	out := filepath.Join(fpga, "default", "out")
	cwd, _ := os.ReadFile(filepath.Join(root, "cwd.log"))
	for _, ln := range strings.Split(strings.TrimSpace(string(cwd)), "\n") {
		if real, _ := filepath.EvalSymlinks(ln); real != mustReal(out) {
			t.Fatalf("tool ran in %s, want %s", ln, out)
		}
	}
	if res.RBF != filepath.Join(out, "default.rbf") || res.RBFSize != RBFBytes {
		t.Fatalf("rbf = %s (%d)", res.RBF, res.RBFSize)
	}
	if strings.Join(res.Sources, ",") != "sync.v,default.v" {
		t.Fatalf("sources = %v", res.Sources)
	}
	for _, f := range []string{"sync.v", "default.v", "regs.vh", "default.sdc", "default.qsf", "default.qpf", "quartus_fit.log", ".gitignore"} {
		if _, err := os.Stat(filepath.Join(out, f)); err != nil {
			t.Fatalf("staged file %s missing: %v", f, err)
		}
	}
	if _, err := os.Stat(filepath.Join(out, "tb.v")); err == nil {
		t.Fatal("common/sim/tb.v must not be staged")
	}
	if qpf, _ := os.ReadFile(filepath.Join(out, "default.qpf")); !strings.Contains(string(qpf), `PROJECT_REVISION = "default"`) {
		t.Fatalf("qpf = %q", qpf)
	}
	if len(res.Stages) != 5 || res.Stages[4].Tool != "quartus_cpf" {
		t.Fatalf("stages = %+v", res.Stages)
	}
	if d := res.Defects(); len(d) != 0 {
		t.Fatalf("defects on a clean flow: %v", d)
	}
	if res.Timing.WorstSetup["pll|clk[0]"] != 0.350 {
		t.Fatalf("timing not parsed: %+v", res.Timing)
	}
	if res.Fit.Summary["M9Ks"] != "44 / 46 ( 96 % )" {
		t.Fatalf("fit summary not parsed: %+v", res.Fit.Summary)
	}
	// the lock must be released after Run
	f, err := os.OpenFile(c.LockPath, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("lock still held after Run: %v", err)
	}
	if !strings.Contains(log.String(), "run: quartus_map default") {
		t.Fatalf("log missing progress lines:\n%s", log.String())
	}
}

// TRLC-LINKS: REQ-SDS-152
func mustReal(p string) string {
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		return p
	}
	return r
}

// TRLC-LINKS: REQ-SDS-152, REQ-SDS-154
func TestRunReportsDefects(t *testing.T) {
	root := fakeQuartus(t)
	fpga := fakeDesign(t)
	t.Setenv("FAKE_FIT", "unassigned")
	t.Setenv("FAKE_STA", "neg")
	res, err := Run(context.Background(), config(t, root, fpga))
	if err != nil {
		t.Fatalf("Run must succeed and carry report defects, got error: %v", err)
	}
	d := strings.Join(res.Defects(), "\n")
	if !strings.Contains(d, "pin unassigned: lane[3]") || !strings.Contains(d, "clock pll|clk[0] worst setup slack -0.123") {
		t.Fatalf("defects = %q", d)
	}
}

// TRLC-LINKS: REQ-SDS-152, REQ-SDS-154
func TestRunNoSTATables(t *testing.T) {
	root := fakeQuartus(t)
	fpga := fakeDesign(t)
	t.Setenv("FAKE_STA", "none")
	res, err := Run(context.Background(), config(t, root, fpga))
	if err != nil {
		t.Fatal(err)
	}
	if d := res.Defects(); len(d) != 1 || !strings.Contains(d[0], "no Setup Summary") {
		t.Fatalf("defects = %v", d)
	}
}

// TRLC-LINKS: REQ-SDS-152
func TestRunWrongRBFSize(t *testing.T) {
	root := fakeQuartus(t)
	fpga := fakeDesign(t)
	t.Setenv("FAKE_RBF_BYTES", "123456")
	_, err := Run(context.Background(), config(t, root, fpga))
	if err == nil || !strings.Contains(err.Error(), "123456 bytes, want 368011") {
		t.Fatalf("err = %v", err)
	}
}

// TRLC-LINKS: REQ-SDS-152
func TestRunToolFailure(t *testing.T) {
	root := fakeQuartus(t)
	fpga := fakeDesign(t)
	t.Setenv("FAKE_FAIL_TOOL", "quartus_fit")
	res, err := Run(context.Background(), config(t, root, fpga))
	if err == nil || !strings.Contains(err.Error(), "quartus_fit failed") || !strings.Contains(err.Error(), "fake quartus_fit failure") {
		t.Fatalf("err = %v", err)
	}
	if got := calls(t, root); len(got) != 2 {
		t.Fatalf("flow must stop at the failing tool, calls = %v", got)
	}
	if res == nil || len(res.Stages) != 2 {
		t.Fatalf("partial result must carry the stages run: %+v", res)
	}
}

// TRLC-LINKS: REQ-SDS-152
func TestRunMemoryGate(t *testing.T) {
	root := fakeQuartus(t)
	fpga := fakeDesign(t)
	c := config(t, root, fpga)
	c.MemInfo = meminfo(t, 2048)
	_, err := Run(context.Background(), c)
	if err == nil || !strings.Contains(err.Error(), "only 2048 MB available (< 3072 MB)") {
		t.Fatalf("err = %v", err)
	}
	if got := calls(t, root); len(got) != 0 {
		t.Fatalf("no tool may run below the memory floor, calls = %v", got)
	}
	// the gate can be disabled explicitly
	c.MinFreeMB = -1
	if _, err := Run(context.Background(), c); err != nil {
		t.Fatalf("MinFreeMB<0 must disable the gate: %v", err)
	}
}

// TRLC-LINKS: REQ-SDS-152
func TestRunLockHeld(t *testing.T) {
	root := fakeQuartus(t)
	fpga := fakeDesign(t)
	c := config(t, root, fpga)
	c.LockWait = 700 * time.Millisecond
	held, err := AcquireLock(context.Background(), c.LockPath, 0, "test-holder")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Release()
	t0 := time.Now()
	_, err = Run(context.Background(), c)
	if err == nil || !strings.Contains(err.Error(), "another Quartus flow holds") || !strings.Contains(err.Error(), "test-holder") {
		t.Fatalf("err = %v", err)
	}
	if time.Since(t0) < 600*time.Millisecond {
		t.Fatalf("did not wait for the lock (%s)", time.Since(t0))
	}
	if got := calls(t, root); len(got) != 0 {
		t.Fatalf("no tool may run without the lock, calls = %v", got)
	}
	held.Release()
	if _, err := Run(context.Background(), c); err != nil {
		t.Fatalf("after release the flow must run: %v", err)
	}
}

// TRLC-LINKS: REQ-SDS-153
func TestRunStaleQSF(t *testing.T) {
	root := fakeQuartus(t)
	fpga := fakeDesign(t)
	if err := os.WriteFile(filepath.Join(fpga, "default", "capture.v"), []byte("module capture; endmodule\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Run(context.Background(), config(t, root, fpga))
	if err == nil || !strings.Contains(err.Error(), "VERILOG_FILE list is stale") || !strings.Contains(err.Error(), "capture.v") {
		t.Fatalf("err = %v", err)
	}
	if got := calls(t, root); len(got) != 0 {
		t.Fatalf("calls = %v", got)
	}
}

// TRLC-LINKS: REQ-SDS-153
func TestRunMissingSDC(t *testing.T) {
	root := fakeQuartus(t)
	fpga := fakeDesign(t)
	if err := os.Remove(filepath.Join(fpga, "default", "default.sdc")); err != nil {
		t.Fatal(err)
	}
	_, err := Run(context.Background(), config(t, root, fpga))
	if err == nil || !strings.Contains(err.Error(), "SDC_FILE default.sdc") {
		t.Fatalf("err = %v", err)
	}
}

// TRLC-LINKS: REQ-SDS-153
func TestRunBasenameCollision(t *testing.T) {
	root := fakeQuartus(t)
	fpga := fakeDesign(t)
	if err := os.WriteFile(filepath.Join(fpga, "default", "sync.v"), []byte("module sync2; endmodule\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Run(context.Background(), config(t, root, fpga))
	if err == nil || !strings.Contains(err.Error(), "both stage as sync.v") {
		t.Fatalf("err = %v", err)
	}
}

// TRLC-LINKS: REQ-SDS-153
func TestRunMissingQSF(t *testing.T) {
	root := fakeQuartus(t)
	c := config(t, root, t.TempDir())
	if _, err := Run(context.Background(), c); err == nil || !strings.Contains(err.Error(), "default.qsf") {
		t.Fatalf("err = %v", err)
	}
}

// TRLC-LINKS: REQ-SDS-152
func TestConfigRequired(t *testing.T) {
	if _, err := Run(context.Background(), Config{}); err == nil {
		t.Fatal("empty config must be rejected")
	}
}

// TestRealReports parses real Quartus 21.1 reports from the read-only owned-fpga
// reference tree when it is present on this host (skipped elsewhere). The
// standard design's fit report has exactly one unplaced pin (adc_lane[13]) and
// the adcstrap STA report three setup-summary corners for clock "clk".
// TRLC-LINKS: REQ-SDS-154
func TestRealReports(t *testing.T) {
	const ref = "/home/labeth/ws/open-sds1000cml/fpga"
	fit, err := os.ReadFile(filepath.Join(ref, "standard", "output_files", "acq.fit.rpt"))
	if err != nil {
		t.Skip("owned-fpga reference reports not present:", err)
	}
	qsf, err := os.ReadFile(filepath.Join(ref, "standard", "acq.qsf"))
	if err != nil {
		t.Skip(err)
	}
	pins := QSFPins(string(qsf))
	if len(pins) != 98 {
		t.Fatalf("acq.qsf pins = %d, want 98", len(pins))
	}
	r := ParseFitReport(string(fit), pins)
	if r.Summary["Total logic elements"] != "8,259 / 10,320 ( 80 % )" || r.Summary["M9Ks"] != "46 / 46 ( 100 % )" {
		t.Fatalf("summary = %v", r.Summary)
	}
	if len(r.Problems) != 1 || r.Problems[0].Kind != "unassigned" || r.Problems[0].Port != "adc_lane[13]" {
		t.Fatalf("problems = %v", r.Problems)
	}
	if r.PinUsage["L6"] != "gpmc_wait" || r.PinUsage["D2"] != "~ALTERA_FLASH_nCE_nCSO~ / RESERVED_INPUT_WITH_WEAK_PULLUP" {
		t.Fatalf("pin usage L6=%q D2=%q", r.PinUsage["L6"], r.PinUsage["D2"])
	}
	sta, err := os.ReadFile(filepath.Join(ref, "adcstrap", "output_files", "adcstrap.sta.rpt"))
	if err != nil {
		t.Skip(err)
	}
	tr := ParseSTAReport(string(sta))
	if tr.Tables != 3 || tr.WorstSetup["clk"] != 2.972 || tr.Corner["clk"] != "Slow 1200mV 85C" {
		t.Fatalf("sta: %+v", tr)
	}
}
