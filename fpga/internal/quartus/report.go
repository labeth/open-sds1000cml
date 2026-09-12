package quartus

import (
	"bufio"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// This file is pure text parsing of Quartus 21.1 report files (verified against
// the shape of real Cyclone IV E .fit.rpt / .sta.rpt files from the owned-fpga
// branch; the driver's own tests use synthetic reports in the same format).

// PinProblem is one defect the fitter report shows for a ball or port the QSF
// names: a port with no location (the fitter chose one) or a ball whose QSF
// assignment did not end up on the port it names (typically the port does not
// exist in the design, so the ball fell back to RESERVED_INPUT).
type PinProblem struct {
	Kind   string // "unassigned" | "unused" | "message"
	Port   string // top-level port name (may be empty for a raw message)
	Ball   string // package ball (may be empty)
	Detail string
}

func (p PinProblem) String() string {
	switch {
	case p.Port != "" && p.Ball != "":
		return fmt.Sprintf("%s: %s @ %s: %s", p.Kind, p.Port, p.Ball, p.Detail)
	case p.Port != "":
		return fmt.Sprintf("%s: %s: %s", p.Kind, p.Port, p.Detail)
	case p.Ball != "":
		return fmt.Sprintf("%s: %s: %s", p.Kind, p.Ball, p.Detail)
	}
	return fmt.Sprintf("%s: %s", p.Kind, p.Detail)
}

// FitReport is what the driver extracts from <project>.fit.rpt.
type FitReport struct {
	// Summary holds the Fitter Summary rows worth printing (Total logic
	// elements, Total pins, Total memory bits, Total PLLs, M9Ks ...).
	Summary map[string]string
	// PinUsage maps every ball in the All Package Pins table to its
	// "Pin Name/Usage" cell.
	PinUsage map[string]string
	// Problems lists unassigned / unused pin defects against the QSF's pins.
	Problems []PinProblem
	// Warnings lists Critical Warning / Warning lines of interest (I/O related).
	Warnings []string
}

// TimingReport is what the driver extracts from <project>.sta.rpt.
type TimingReport struct {
	// WorstSetup is the minimum setup slack per clock across every
	// "<corner> Model Setup Summary" table (ns). Negative = timing failure.
	WorstSetup map[string]float64
	// Corner names the corner that produced each clock's worst value.
	Corner map[string]string
	// Tables counts the Setup Summary tables found (0 = the report had none:
	// no SDC, or STA did not run).
	Tables int
}

// Clocks returns the clock names sorted.
func (t TimingReport) Clocks() []string {
	var cs []string
	for c := range t.WorstSetup {
		cs = append(cs, c)
	}
	sort.Strings(cs)
	return cs
}

// Failing returns the clocks whose worst setup slack is negative.
func (t TimingReport) Failing() []string {
	var f []string
	for _, c := range t.Clocks() {
		if t.WorstSetup[c] < 0 {
			f = append(f, c)
		}
	}
	return f
}

var locationRE = regexp.MustCompile(`^\s*set_location_assignment\s+PIN_([A-Z]+[0-9]+)\s+-to\s+(\S+)`)

// QSFPins returns ball -> port for every set_location_assignment PIN_x -to y
// line of a QSF (comment lines ignored).
func QSFPins(qsf string) map[string]string {
	out := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(qsf))
	for sc.Scan() {
		ln := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(ln, "#") {
			continue
		}
		if m := locationRE.FindStringSubmatch(ln); m != nil {
			out[m[1]] = strings.Trim(m[2], "\"")
		}
	}
	return out
}

// QSFGlobal returns the value of a set_global_assignment -name NAME value line,
// or "" if absent. Quotes are stripped.
func QSFGlobal(qsf, name string) string {
	needle := "-name " + name + " "
	sc := bufio.NewScanner(strings.NewReader(qsf))
	for sc.Scan() {
		ln := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(ln, "#") || !strings.HasPrefix(ln, "set_global_assignment") {
			continue
		}
		if i := strings.Index(ln, needle); i >= 0 {
			return strings.Trim(strings.TrimSpace(ln[i+len(needle):]), "\"")
		}
	}
	return ""
}

// QSFGlobals returns every value of a repeated global assignment (VERILOG_FILE).
func QSFGlobals(qsf, name string) []string {
	needle := "-name " + name + " "
	var out []string
	sc := bufio.NewScanner(strings.NewReader(qsf))
	for sc.Scan() {
		ln := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(ln, "#") || !strings.HasPrefix(ln, "set_global_assignment") {
			continue
		}
		if i := strings.Index(ln, needle); i >= 0 {
			out = append(out, strings.Trim(strings.TrimSpace(ln[i+len(needle):]), "\""))
		}
	}
	return out
}

// tables splits a Quartus report into its named ";"-delimited tables: the title
// row "; Name ;" followed by rows of cells. Returns title -> rows (each row a
// slice of trimmed cells). A title that appears more than once keeps the first.
func tables(text string) map[string][][]string {
	out := map[string][][]string{}
	lines := strings.Split(text, "\n")
	for i := 0; i < len(lines); i++ {
		ln := lines[i]
		if !strings.HasPrefix(ln, "; ") || i == 0 || !strings.HasPrefix(lines[i-1], "+-") {
			continue
		}
		// a title row has exactly one cell and the next line is a +- rule
		cells := splitCells(ln)
		if len(cells) != 1 || i+1 >= len(lines) || !strings.HasPrefix(lines[i+1], "+-") {
			continue
		}
		title := cells[0]
		var rows [][]string
		for j := i + 2; j < len(lines); j++ {
			r := lines[j]
			if strings.HasPrefix(r, "+-") {
				continue
			}
			if !strings.HasPrefix(r, ";") {
				break
			}
			rows = append(rows, splitCells(r))
		}
		if _, dup := out[title]; !dup {
			out[title] = rows
		}
	}
	return out
}

func splitCells(row string) []string {
	row = strings.TrimSpace(row)
	row = strings.TrimPrefix(row, ";")
	row = strings.TrimSuffix(row, ";")
	parts := strings.Split(row, ";")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

var fitSummaryKeys = []string{"Total logic elements", "Total registers", "Total pins", "Total memory bits", "Embedded Multiplier 9-bit elements", "Total PLLs", "M9Ks"}

// Quartus indents nested messages ("    Warning (15706): ..."), hence \s*.
var fitWarningRE = regexp.MustCompile(`^\s*(Critical Warning|Warning) \((\d+)\): (.*)$`)

// warning codes that concern pins / IO and are worth surfacing verbatim
var fitWarningCodes = map[string]bool{
	"169085": true, // No exact pin location assignment(s) for N pins
	"169086": true, // Pin X not assigned to an exact location
	"15705":  true, // Ignored locations or region assignments to the following nodes
	"15706":  true, // Node X is assigned to location or region, but does not exist in design
	"169064": true, // pins have no output enable or a permanently disabled output enable
	"15714":  true, // Some pins have incomplete I/O assignments
	"169177": true, // pins must meet requirements for 3.3-V interfaces (AN 447)
	"176250": true, // Ignoring invalid fast I/O register assignments
	"13024":  true, // (map) Output pins are stuck at VCC or GND
	"332060": true, // Node: ... was determined to be a clock but was found without an associated clock assignment
}

var ignoredNodeRE = regexp.MustCompile(`Node "([^"]+)" is assigned to location or region, but does not exist`)
var notPlacedRE = regexp.MustCompile(`Pin ([^ ]+) not assigned to an exact location`)

// ParseFitReport extracts the summary, the package-pin usage table and the
// pin defects for the QSF pins (ball -> port) from a .fit.rpt text.
func ParseFitReport(text string, qsfPins map[string]string) FitReport {
	r := FitReport{Summary: map[string]string{}, PinUsage: map[string]string{}}
	tb := tables(text)

	if rows, ok := tb["Fitter Summary"]; ok {
		for _, row := range rows {
			if len(row) < 2 {
				continue
			}
			for _, k := range fitSummaryKeys {
				if row[0] == k {
					r.Summary[k] = row[1]
				}
			}
		}
	}
	// the M9K count lives in the Fitter Resource Usage Summary
	if rows, ok := tb["Fitter Resource Usage Summary"]; ok {
		for _, row := range rows {
			if len(row) >= 2 && row[0] == "M9Ks" {
				r.Summary["M9Ks"] = row[1]
			}
		}
	}

	if rows, ok := tb["All Package Pins"]; ok {
		for _, row := range rows {
			if len(row) >= 4 && row[0] != "Location" {
				r.PinUsage[row[0]] = row[3]
			}
		}
	}

	seen := map[string]bool{}
	add := func(p PinProblem) {
		k := p.String()
		if !seen[k] {
			seen[k] = true
			r.Problems = append(r.Problems, p)
		}
	}

	// 1. ports the fitter had to place itself
	if rows, ok := tb["I/O Assignment Warnings"]; ok {
		for _, row := range rows {
			if len(row) >= 2 && row[0] != "Pin Name" {
				kind := "message"
				if strings.Contains(row[1], "location") {
					kind = "unassigned"
				}
				add(PinProblem{Kind: kind, Port: row[0], Detail: row[1]})
			}
		}
	}

	// 2. every QSF ball must carry exactly the port the QSF names
	balls := make([]string, 0, len(qsfPins))
	for b := range qsfPins {
		balls = append(balls, b)
	}
	sort.Strings(balls)
	if len(r.PinUsage) > 0 {
		for _, b := range balls {
			port := qsfPins[b]
			usage, ok := r.PinUsage[b]
			if !ok {
				add(PinProblem{Kind: "unused", Port: port, Ball: b, Detail: "ball absent from the All Package Pins table"})
				continue
			}
			if !usageNames(usage, port) {
				add(PinProblem{Kind: "unused", Port: port, Ball: b, Detail: "fitter reports " + usage + " (assignment ignored: port not in the design?)"})
			}
		}
	} else if len(balls) > 0 {
		add(PinProblem{Kind: "message", Detail: "fit report has no All Package Pins table; per-ball usage not verified"})
	}

	// 3. verbatim messages of interest
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		ln := sc.Text()
		m := fitWarningRE.FindStringSubmatch(ln)
		if m == nil {
			continue
		}
		if fitWarningCodes[m[2]] {
			r.Warnings = append(r.Warnings, ln)
		}
		if mm := ignoredNodeRE.FindStringSubmatch(ln); mm != nil {
			add(PinProblem{Kind: "unused", Port: mm[1], Detail: "assignment to a node that does not exist in the design"})
		}
		if mm := notPlacedRE.FindStringSubmatch(ln); mm != nil {
			add(PinProblem{Kind: "unassigned", Port: mm[1], Detail: "not assigned to an exact location"})
		}
	}
	return r
}

// usageNames reports whether a "Pin Name/Usage" cell names the port: either
// exactly, or as one of the " / "-joined names (dual-purpose balls read
// "~ALTERA_FLASH_nCE_nCSO~ / d2").
func usageNames(usage, port string) bool {
	if usage == port {
		return true
	}
	for _, part := range strings.Split(usage, " / ") {
		if strings.TrimSpace(part) == port {
			return true
		}
	}
	return false
}

// ParseSTAReport folds every "<corner> Model Setup Summary" table into the
// worst setup slack per clock.
//
// The title row is one cell, ";<title>;", and Quartus pads the title with
// spaces to the table's width whenever a clock name is longer than the title
// ("; Slow 1200mV 85C Model Setup Summary                    ;" on a design
// with PLL-generated clock names) -- so the title is matched on the trimmed
// cell, never on the raw line.
const staSetupTitle = " Model Setup Summary"

func ParseSTAReport(text string) TimingReport {
	t := TimingReport{WorstSetup: map[string]float64{}, Corner: map[string]string{}}
	lines := strings.Split(text, "\n")
	for i := 0; i+1 < len(lines); i++ {
		ln := lines[i]
		if !strings.HasPrefix(ln, "; ") {
			continue
		}
		title := splitCells(ln)
		if len(title) != 1 || !strings.HasSuffix(title[0], staSetupTitle) {
			continue
		}
		if i == 0 || !strings.HasPrefix(lines[i-1], "+-") || !strings.HasPrefix(lines[i+1], "+-") {
			continue
		}
		corner := strings.TrimSuffix(title[0], staSetupTitle)
		t.Tables++
		for j := i + 2; j < len(lines); j++ {
			r := lines[j]
			if strings.HasPrefix(r, "+-") {
				continue
			}
			if !strings.HasPrefix(r, ";") {
				break
			}
			cells := splitCells(r)
			if len(cells) < 2 || cells[0] == "Clock" {
				continue
			}
			slack, err := strconv.ParseFloat(cells[1], 64)
			if err != nil {
				continue
			}
			if cur, ok := t.WorstSetup[cells[0]]; !ok || slack < cur {
				t.WorstSetup[cells[0]] = slack
				t.Corner[cells[0]] = corner
			}
		}
	}
	return t
}
