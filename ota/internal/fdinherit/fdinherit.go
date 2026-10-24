// Package fdinherit discovers inherited file descriptors and device-node
// holders via /proc.
//
// The load-bearing contract (spec 01 §2.3/§5, spec 09 preamble): /dev/Gpmc and
// /dev/fpga_key are opened once by the boot chain and passed down the process
// tree; a fresh open() faults, and closing the inherited descriptor frees the
// chip select for the whole tree. The agent therefore keeps raw integer fds —
// never wrapped in an *os.File — so no finalizer or Close path can ever touch
// them.
// ENGMODEL-OWNER-UNIT: FU-OTA-FDINHERIT
package fdinherit

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// Find returns the number of the inherited descriptor whose /proc/self/fd
// readlink target equals path, or -1 if not inherited.
// TRLC-LINKS: REQ-SDS-002
func Find(path string) int {
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return -1
	}
	for _, e := range entries {
		fd, err := strconv.Atoi(e.Name())
		if err != nil || fd < 3 { // skip stdio
			continue
		}
		target, err := os.Readlink(filepath.Join("/proc/self/fd", e.Name()))
		if err == nil && target == path {
			return fd
		}
	}
	return -1
}

// Holder describes a process holding an open descriptor to a device node.
// TRLC-LINKS: REQ-SDS-085
type Holder struct {
	PID  int    `json:"pid"`
	Comm string `json:"comm"`
	Exe  string `json:"exe"` // may be empty if unreadable
}

// HoldersOf scans /proc/*/fd for descriptors pointing at path.
// TRLC-LINKS: REQ-SDS-085
func HoldersOf(path string) []Holder { return HoldersOfEach(path)[0] }

// HoldersOfEach is HoldersOf for several paths in one /proc scan: the scan
// is a readlink per descriptor of every process, and a probe asks for three
// device nodes.
// TRLC-LINKS: REQ-SDS-085
func HoldersOfEach(paths ...string) [][]Holder {
	out := make([][]Holder, len(paths))
	d, err := os.Open("/proc")
	if err != nil {
		return out
	}
	procs, err := d.Readdirnames(-1)
	d.Close()
	if err != nil {
		return out
	}
	var link [512]byte
	for _, p := range procs {
		pid, err := strconv.Atoi(p)
		if err != nil {
			continue
		}
		fdDir := "/proc/" + p + "/fd/"
		fd, err := os.Open(fdDir)
		if err != nil {
			continue // vanished or not ours to read
		}
		fds, err := fd.Readdirnames(-1)
		fd.Close()
		if err != nil {
			continue
		}
		var h *Holder
		left := len(paths)
		matched := make([]bool, len(paths))
		for _, f := range fds {
			n, err := syscall.Readlink(fdDir+f, link[:])
			if err != nil || n >= len(link) {
				continue
			}
			for i, path := range paths {
				if matched[i] || string(link[:n]) != path {
					continue
				}
				if h == nil {
					h = &Holder{PID: pid, Comm: Comm(pid), Exe: Exe(pid)}
				}
				out[i] = append(out[i], *h)
				matched[i] = true
				left--
			}
			if left == 0 {
				break
			}
		}
	}
	return out
}

// Comm returns /proc/<pid>/comm trimmed, or "".
// TRLC-LINKS: REQ-SDS-085
func Comm(pid int) string {
	b, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "comm"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// Exe returns the readlink of /proc/<pid>/exe, or "".
// TRLC-LINKS: REQ-SDS-085
func Exe(pid int) string {
	s, err := os.Readlink(filepath.Join("/proc", strconv.Itoa(pid), "exe"))
	if err != nil {
		return ""
	}
	return s
}

// PPid returns the parent pid from /proc/<pid>/status, or -1.
// TRLC-LINKS: REQ-SDS-085
func PPid(pid int) int {
	b, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "status"))
	if err != nil {
		return -1
	}
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(line, "PPid:"); ok {
			n, err := strconv.Atoi(strings.TrimSpace(v))
			if err != nil {
				return -1
			}
			return n
		}
	}
	return -1
}

// AncestorsOfSelf returns the pid set of this process's ancestor chain
// (parent, grandparent, … up to init), self excluded.
// TRLC-LINKS: REQ-SDS-085
func AncestorsOfSelf() map[int]bool {
	out := map[int]bool{}
	pid := os.Getpid()
	for i := 0; i < 64; i++ { // bounded against /proc races
		pid = PPid(pid)
		if pid <= 0 || out[pid] {
			break
		}
		out[pid] = true
		if pid == 1 {
			break
		}
	}
	return out
}

// DescendantsOfSelf returns the pid set of this process's descendants.
// TRLC-LINKS: REQ-SDS-085
func DescendantsOfSelf() map[int]bool {
	children := map[int][]int{}
	procs, _ := os.ReadDir("/proc")
	for _, p := range procs {
		pid, err := strconv.Atoi(p.Name())
		if err != nil {
			continue
		}
		if pp := PPid(pid); pp > 0 {
			children[pp] = append(children[pp], pid)
		}
	}
	out := map[int]bool{}
	stack := []int{os.Getpid()}
	for len(stack) > 0 {
		p := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, c := range children[p] {
			if !out[c] {
				out[c] = true
				stack = append(stack, c)
			}
		}
	}
	return out
}

// Alive reports whether /proc/<pid> still exists.
// TRLC-LINKS: REQ-SDS-085
func Alive(pid int) bool {
	_, err := os.Stat(filepath.Join("/proc", strconv.Itoa(pid)))
	return err == nil
}
