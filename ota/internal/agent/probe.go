package agent

import (
	"strings"
	"time"

	"open-sds/ota/internal/fdinherit"
	"open-sds/ota/internal/gpmc"
	"open-sds/ota/internal/sysinfo"
)

// ProbeReport is the first-session, read-only device fingerprint. It answers
// the open platform questions the specs leave to on-device verification:
// kernel, network config, factory-app identity, watchdog/portmap holders,
// mount points, and (opt-in) a safe GPMC version/fill read.
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

// Probe is the exported entry for the `agent probe` subcommand.
func (a *Agent) Probe(readGpmc bool) ProbeReport { return a.probe(readGpmc) }

func (a *Agent) probe(readGpmc bool) ProbeReport {
	r := ProbeReport{
		Device:      a.cfg.DeviceID,
		Uname:       sysinfo.Uname(),
		Sys:         sysinfo.Collect(a.cfg.OTADir),
		InheritedFD: map[string]int{"gpmc": a.gpmcFD, "fpga_key": a.fpgaKeyFD},
		GpmcHolders: fdinherit.HoldersOf(a.cfg.GpmcDev),
		KeyHolders:  fdinherit.HoldersOf(a.cfg.FpgaKeyDev),
		WdHolders:   fdinherit.HoldersOf(a.cfg.WatchdogDev),
		FactoryCand: a.factoryCandidates(),
		TakenOver:   a.st.get().TakenOver,
		Cmds:        map[string]string{},
	}
	// Read-only environment queries, surfaced through `otactl probe`. Each is
	// bounded and best-effort; missing applets simply yield an error string.
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
		out, _ := a.runShell(script, 6*time.Second)
		r.Cmds[name] = strings.TrimSpace(string(out))
	}

	if readGpmc && a.gpmc.OK() {
		if v, ok := a.gpmc.VerifyVersion(); ok {
			r.GpmcVersion = "0x0052 (verified)"
		} else {
			r.GpmcVersion = hex4(v) + " (UNEXPECTED — want 0x0052)"
		}
		if fill, err := a.gpmc.Read(gpmc.PlaneCS1, gpmc.SelFill); err == nil {
			r.GpmcFill = hex4(fill & gpmc.FillMask)
		}
	}
	return r
}

func hex4(v uint16) string {
	const d = "0123456789abcdef"
	return "0x" + string([]byte{d[v>>12&0xf], d[v>>8&0xf], d[v>>4&0xf], d[v&0xf]})
}
