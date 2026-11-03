// ENGMODEL-OWNER-UNIT: FU-OTA-DIAG

package diag

import (
	"bufio"
	"compress/gzip"
	"fmt"
	"os"
	"sort"
	"strings"
)

// regUse is how the factory app used one FPGA register across the sweep.
type regUse struct {
	reads, writes int
	values        map[string]bool // values written
	steps         map[int]bool    // steps that wrote it
}

// regMap is the factory app's register traffic by plane and selector: the
// FPGA side of the board as its factory firmware drives it.
type regMap map[string]*regUse

// addTrace counts the GPMC lines of one step's trace file (gzip'd).
// TRLC-LINKS: REQ-SDS-285
func (m regMap) addTrace(step int, path string) error {
	f, err := os.Open(path) //nolint:gosec // G304: the step's trace file on the stick
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	sc := bufio.NewScanner(gz)
	for sc.Scan() {
		// "<t> <tid> gpmc <R|W> <plane> <sel> <val> ret=<r>"
		fs := strings.Fields(sc.Text())
		if len(fs) < 7 || fs[2] != "gpmc" {
			continue
		}
		key := "CS" + fs[4] + " " + fs[5]
		u := m[key]
		if u == nil {
			u = &regUse{values: map[string]bool{}, steps: map[int]bool{}}
			m[key] = u
		}
		if fs[3] == "W" {
			u.writes++
			u.values[fs[6]] = true
			u.steps[step] = true
		} else {
			u.reads++
		}
	}
	return sc.Err()
}

// text is the map as a table, one register a line: reads, writes, the values
// written (at most 16) and the sweep steps that wrote it.
// TRLC-LINKS: REQ-SDS-285
func (m regMap) text(steps []Step) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	fmt.Fprintf(&b, "# register (plane selector)  reads  writes  values written  the steps that wrote it\n")
	for _, k := range keys {
		u := m[k]
		vals := make([]string, 0, len(u.values))
		for v := range u.values {
			vals = append(vals, v)
		}
		sort.Strings(vals)
		more := ""
		if len(vals) > 16 {
			vals, more = vals[:16], fmt.Sprintf(" +%d", len(u.values)-16)
		}
		var by []string
		for i := range steps {
			if u.steps[i] {
				by = append(by, fmt.Sprintf("%d(%s)", i, steps[i].Group))
			}
		}
		fmt.Fprintf(&b, "%-12s %7d %7d  %s%s  %s\n", k, u.reads, u.writes, strings.Join(vals, ","), more, strings.Join(by, " "))
	}
	return b.String()
}
