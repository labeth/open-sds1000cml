// ENGMODEL-OWNER-UNIT: FU-OTA-DIAG

package diag

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Proc is one process as the diagnostics record it.
type Proc struct {
	PID     int      `json:"pid"`
	PPID    int      `json:"ppid"`
	Comm    string   `json:"comm"`
	Exe     string   `json:"exe,omitempty"`
	Cmdline []string `json:"cmdline,omitempty"`
	FDs     []string `json:"fds,omitempty"` // fd -> target
}

// factoryApp is the vendor app's executable name.
const factoryApp = "SDS1000_arm.app"

// collectProcs stores every process's identity, open files and memory map:
// the factory app's maps show the device windows it maps from /dev/mem and
// its descriptors the devices it drives.
// TRLC-LINKS: REQ-SDS-284
func collectProcs(a *Archive) []Proc {
	ents, err := os.ReadDir("/proc")
	if err != nil {
		a.Errorf("/proc: %v", err)
		return nil
	}
	var ps []Proc
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		dir := "/proc/" + e.Name()
		p := Proc{PID: pid, Comm: strings.TrimSpace(readStr(dir + "/comm"))}
		p.Exe, _ = os.Readlink(dir + "/exe")
		if raw := strings.TrimRight(readStr(dir+"/cmdline"), "\x00"); raw != "" {
			p.Cmdline = strings.Split(raw, "\x00")
		}
		for l := range strings.SplitSeq(readStr(dir+"/status"), "\n") {
			if v, ok := strings.CutPrefix(l, "PPid:"); ok {
				p.PPID, _ = strconv.Atoi(strings.TrimSpace(v))
			}
		}
		fds, _ := os.ReadDir(dir + "/fd")
		for _, fd := range fds {
			t, _ := os.Readlink(filepath.Join(dir, "fd", fd.Name()))
			p.FDs = append(p.FDs, fd.Name()+" -> "+t)
		}
		sort.Strings(p.FDs)
		a.AddFile(fmt.Sprintf("procs/%d/maps", pid), dir+"/maps", 1<<20)
		a.AddFile(fmt.Sprintf("procs/%d/status", pid), dir+"/status", 64<<10)
		a.AddFile(fmt.Sprintf("procs/%d/environ", pid), dir+"/environ", 64<<10)
		ps = append(ps, p)
	}
	a.AddJSON("procs/procs.json", ps)
	return ps
}

// factoryPID is the running factory app's pid, or 0.
// TRLC-LINKS: REQ-SDS-284
func factoryPID(ps []Proc) int {
	for _, p := range ps {
		if strings.HasSuffix(p.Exe, "/"+factoryApp) || p.Comm == factoryApp[:min(len(factoryApp), 15)] { // comm holds 15 characters
			return p.PID
		}
	}
	return 0
}

// readStr is a small /proc file's text, or "".
// TRLC-LINKS: REQ-SDS-284
func readStr(path string) string {
	b, err := os.ReadFile(path) //nolint:gosec // G304: a /proc file of a listed process
	if err != nil {
		return ""
	}
	return string(b)
}
