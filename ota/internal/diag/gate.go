// ENGMODEL-OWNER-UNIT: FU-OTA-DIAG

package diag

import (
	"fmt"
	"slices"
	"strings"
)

// Check is one condition the active stage needs, as the passive stage found it.
type Check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

// gateInput is what the passive stage learned that the gate judges.
type gateInput struct {
	cpuinfo string
	procs   []Proc
	pid     int    // the factory app, 0 if none runs
	fpgaOK  bool   // the inherited /dev/Gpmc answered a register read
	fpgaVer uint16 // the version register's value
	scpi    *scpiState
}

// Gate is the checks the active stage needs before it drives the factory
// app: a scope of the family its trace and sweep understand (an AM335x
// running the SDS1000 factory app on /dev/Gpmc, answering Siglent SCPI in
// the form the sweep reads back), so it drives nothing it was not made for.
// The FPGA version itself is not checked: another population has its own.
// TRLC-LINKS: REQ-SDS-287
func Gate(in gateInput) []Check {
	var cs []Check
	add := func(name string, ok bool, f string, args ...any) {
		cs = append(cs, Check{Name: name, OK: ok, Detail: fmt.Sprintf(f, args...)})
	}
	add("cpu", strings.Contains(strings.ToLower(in.cpuinfo), "am335"), "an AM335x (the factory GPMC driver, the trace's ARM syscalls): %s", cpuLine())
	add("factory app", in.pid != 0, "%s running: pid %d", factoryApp, in.pid)
	holds := false
	for _, p := range in.procs {
		if p.PID == in.pid && slices.ContainsFunc(p.FDs, func(fd string) bool { return strings.HasSuffix(fd, " -> /dev/Gpmc") }) {
			holds = true
		}
	}
	add("factory bus", holds, "the factory app holds /dev/Gpmc (the trace decodes that device's register ioctls)")
	add("fpga", in.fpgaOK, "the inherited /dev/Gpmc answers a register read (version register 0x%04x)", in.fpgaVer)
	idn := ""
	if in.scpi != nil {
		idn = in.scpi.Answers["*IDN?"]
	}
	f := strings.Split(idn, ",")
	add("identity", len(f) >= 2 && f[0] == "Siglent" && strings.HasPrefix(f[1], "SDS1"), "a Siglent SDS1000-series scope: %q", idn)
	form := true
	var bad []string
	for _, q := range []string{"C1:VDIV?", "C1:ATTN?", "C1:CPL?", "TDIV?", "TRMD?"} {
		ans := ""
		if in.scpi != nil {
			ans = in.scpi.Answers[q]
		}
		if !strings.HasPrefix(ans, strings.TrimSuffix(q, "?")+" ") {
			form = false
			bad = append(bad, fmt.Sprintf("%s %q", q, ans))
		}
	}
	add("scpi form", form, "the settings answer in the form the sweep reads back and restores %v", bad)
	return cs
}

// passed reports whether every check passed, and the first that did not.
// TRLC-LINKS: REQ-SDS-287
func passed(cs []Check) (bool, string) {
	for _, c := range cs {
		if !c.OK {
			return false, c.Name + ": " + c.Detail
		}
	}
	return true, ""
}
