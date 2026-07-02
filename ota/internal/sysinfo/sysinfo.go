// Package sysinfo collects read-only platform facts for heartbeats and the
// first-session probe (kernel, IPs, memory, mounts, disk).
package sysinfo

import (
	"net"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

type Info struct {
	Uname   string   `json:"uname"`
	IPs     []string `json:"ips"`
	MemFree string   `json:"mem,omitempty"`     // "free/total kB"
	Uptime  string   `json:"uptime,omitempty"`  // seconds, raw /proc/uptime
	Loadavg string   `json:"loadavg,omitempty"` // raw /proc/loadavg
	DiskB   int64    `json:"usb_free_bytes"`    // free bytes on the OTA dir fs
}

func cstr(b []byte) string {
	if i := strings.IndexByte(string(b[:]), 0); i >= 0 {
		return string(b[:i])
	}
	return string(b[:])
}

func Uname() string {
	var u unix.Utsname
	if err := unix.Uname(&u); err != nil {
		return "uname: " + err.Error()
	}
	return strings.Join([]string{
		cstr(u.Sysname[:]), cstr(u.Nodename[:]), cstr(u.Release[:]),
		cstr(u.Version[:]), cstr(u.Machine[:]),
	}, " ")
}

// IPv4s returns "ifname ip/mask" for every non-loopback interface.
func IPv4s() []string {
	var out []string
	ifs, err := net.Interfaces()
	if err != nil {
		return nil
	}
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil {
				out = append(out, ifc.Name+" "+ipn.String())
			}
		}
	}
	return out
}

func firstLine(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	s := string(b)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

func mem() string {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return ""
	}
	var total, free string
	for _, l := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(l, "MemTotal:"); ok {
			total = strings.TrimSpace(v)
		}
		if v, ok := strings.CutPrefix(l, "MemFree:"); ok {
			free = strings.TrimSpace(v)
		}
	}
	return free + " free / " + total
}

func FreeBytes(path string) int64 {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return -1
	}
	return int64(st.Bavail) * int64(st.Bsize)
}

func Collect(otaDir string) Info {
	return Info{
		Uname:   Uname(),
		IPs:     IPv4s(),
		MemFree: mem(),
		Uptime:  firstLine("/proc/uptime"),
		Loadavg: firstLine("/proc/loadavg"),
		DiskB:   FreeBytes(otaDir),
	}
}
