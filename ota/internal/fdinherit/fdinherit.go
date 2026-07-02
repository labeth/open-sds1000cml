// Package fdinherit discovers inherited file descriptors and device-node
// holders via /proc.
//
// The load-bearing contract (spec 01 §2.3/§5, spec 09 preamble): /dev/Gpmc and
// /dev/fpga_key are opened once by the boot chain and passed down the process
// tree; a fresh open() faults, and closing the inherited descriptor frees the
// chip select for the whole tree. The agent therefore keeps raw integer fds —
// never wrapped in an *os.File — so no finalizer or Close path can ever touch
// them.
package fdinherit

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Find returns the number of the inherited descriptor whose /proc/self/fd
// readlink target equals path, or -1 if not inherited.
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
type Holder struct {
	PID  int    `json:"pid"`
	Comm string `json:"comm"`
	Exe  string `json:"exe"` // may be empty if unreadable
}

// HoldersOf scans /proc/*/fd for descriptors pointing at path.
func HoldersOf(path string) []Holder {
	procs, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	var out []Holder
	for _, p := range procs {
		pid, err := strconv.Atoi(p.Name())
		if err != nil {
			continue
		}
		fdDir := filepath.Join("/proc", p.Name(), "fd")
		fds, err := os.ReadDir(fdDir)
		if err != nil {
			continue // vanished or not ours to read
		}
		for _, f := range fds {
			target, err := os.Readlink(filepath.Join(fdDir, f.Name()))
			if err != nil || target != path {
				continue
			}
			out = append(out, Holder{PID: pid, Comm: Comm(pid), Exe: Exe(pid)})
			break
		}
	}
	return out
}

// Comm returns /proc/<pid>/comm trimmed, or "".
func Comm(pid int) string {
	b, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "comm"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// Exe returns the readlink of /proc/<pid>/exe, or "".
func Exe(pid int) string {
	s, err := os.Readlink(filepath.Join("/proc", strconv.Itoa(pid), "exe"))
	if err != nil {
		return ""
	}
	return s
}

// PPid returns the parent pid from /proc/<pid>/status, or -1.
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
func Alive(pid int) bool {
	_, err := os.Stat(filepath.Join("/proc", strconv.Itoa(pid)))
	return err == nil
}
