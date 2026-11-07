// ENGMODEL-OWNER-UNIT: FU-OTA-AGENT

package board

import (
	"strings"
	"sync"
	"time"

	"github.com/labeth/open-dso-core/supervisor/agent"
	"github.com/labeth/open-dso-core/supervisor/fdinherit"
	"github.com/labeth/open-dso-core/supervisor/sysinfo"

	"github.com/labeth/open-sds1000cml/ota/internal/gpmc"
)

// ProbeReport is the first-session, read-only device fingerprint. It answers
// the open platform questions the specs leave to on-device verification:
// kernel, network config, factory-app identity, watchdog/portmap holders,
// mount points, and (opt-in) a safe GPMC version/fill read.
// TRLC-LINKS: REQ-SDS-114
type ProbeReport struct {
	Device      string             `json:"device"`
	Uname       string             `json:"uname"`
	Sys         sysinfo.Info       `json:"sys"`
	InheritedFD map[string]int     `json:"inherited_fds"`
	GpmcHolders []fdinherit.Holder `json:"gpmc_holders"`
	KeyHolders  []fdinherit.Holder `json:"fpga_key_holders"`
	WdHolders   []fdinherit.Holder `json:"watchdog_holders"`
	FactoryCand []fdinherit.Holder `json:"factory_candidates"`
	Cmds        map[string]string  `json:"cmds"` // short read-only command outputs
	GpmcVersion string             `json:"gpmc_version,omitempty"`
	GpmcFill    string             `json:"gpmc_fill,omitempty"`
	TakenOver   bool               `json:"taken_over"`
}

// Probe is the device fingerprint for the probe RPC and `agent probe`.
// TRLC-LINKS: REQ-SDS-114
func (b *Board) Probe(readGpmc, takenOver bool) any { return b.probe(readGpmc, takenOver) }

// TRLC-LINKS: REQ-SDS-114
func (b *Board) probe(readGpmc, takenOver bool) ProbeReport {
	r := ProbeReport{
		Device:      b.cfg.DeviceID,
		Uname:       sysinfo.Uname(),
		Sys:         sysinfo.Collect(b.cfg.OTADir),
		InheritedFD: b.BootFDs(),
		FactoryCand: b.factoryCandidates(),
		TakenOver:   takenOver,
		Cmds:        map[string]string{},
	}
	held := fdinherit.HoldersOfEach(b.bc.GpmcDev, b.bc.FpgaKeyDev, b.bc.WatchdogDev)
	r.GpmcHolders, r.KeyHolders, r.WdHolders = held[0], held[1], held[2]
	// Read-only environment queries, surfaced through `otactl probe`. Each is
	// bounded and best-effort; missing applets simply yield an error string.
	// They run side by side: the report waits for the slowest, not the sum.
	var mu sync.Mutex
	var wg sync.WaitGroup
	for name, script := range map[string]string{
		"cmdline":  "cat /proc/cmdline",
		"ps":       "ps 2>/dev/null | head -60",
		"mounts":   "cat /proc/mounts",
		"ip":       "ifconfig 2>/dev/null || ip addr",
		"route":    "route -n 2>/dev/null || ip route",
		"rpcinfo":  "rpcinfo -p 127.0.0.1 2>/dev/null",
		"listen":   "netstat -ltn 2>/dev/null | head -40",
		"watchdog": "ls -l /dev/watchdog* 2>/dev/null; cat /sys/class/watchdog/watchdog0/timeout 2>/dev/null; cat /sys/class/watchdog/watchdog0/nowayout 2>/dev/null",
		"inittab":  "cat /etc/inittab 2>/dev/null | head -40",
		"busybox":  "busybox 2>&1 | sed -n '1,3p'; echo ---; busybox --list 2>/dev/null | tr '\\n' ' '",
		"devmem":   "cat /sys/kernel/security/lsm 2>/dev/null; grep -i strict /proc/config.gz 2>/dev/null; echo done",
	} {
		wg.Go(func() {
			out, _ := agent.RunShell(script, 6*time.Second)
			mu.Lock()
			r.Cmds[name] = strings.TrimSpace(string(out))
			mu.Unlock()
		})
	}
	wg.Wait()

	if g := b.reader(); readGpmc && g.OK() {
		if v, ok := g.VerifyVersion(); ok {
			r.GpmcVersion = "0x0052 (verified)"
		} else {
			r.GpmcVersion = hex4(v) + " (UNEXPECTED — want 0x0052)"
		}
		if fill, err := g.Read(gpmc.PlaneCS1, gpmc.SelFill); err == nil {
			r.GpmcFill = hex4(fill & gpmc.FillMask)
		}
	}
	return r
}

// TRLC-LINKS: REQ-SDS-114
func hex4(v uint16) string {
	const d = "0123456789abcdef"
	return "0x" + string([]byte{d[v>>12&0xf], d[v>>8&0xf], d[v>>4&0xf], d[v&0xf]})
}
