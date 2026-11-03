// ENGMODEL-OWNER-UNIT: FU-OTA-DIAG

package diag

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

// procFiles are the kernel's view of the board: CPU, memory map, interrupts,
// drivers, partitions. A board-file kernel (no device tree) names its board
// and its peripherals here and in the boot log.
var procFiles = []string{
	"cpuinfo", "version", "cmdline", "meminfo", "iomem", "ioports", "interrupts", "devices", "misc",
	"mtd", "partitions", "mounts", "modules", "filesystems", "swaps", "uptime", "stat", "buddyinfo",
	"kallsyms", "config.gz", "bus/input/devices", "asound/cards", "net/dev",
}

// sysRoots are the sysfs trees whose small attributes are read: the board's
// devices by bus and class, as the kernel registered them.
var sysRoots = []string{
	"/sys/class", "/sys/bus/platform/devices", "/sys/bus/i2c/devices", "/sys/bus/spi/devices",
	"/sys/bus/usb/devices",
}

// sysDevices is the device tree under the class links: a class entry such
// as /sys/class/graphics/fb0 links into it, six or seven levels down.
const sysDevices = "/sys/devices"

// debugRoots are debugfs files that describe the board's wiring: the pin
// multiplexing of every ball, the GPIO lines and their owners, the clocks.
var debugRoots = []string{
	"/sys/kernel/debug/omap_mux", "/sys/kernel/debug/gpio", "/sys/kernel/debug/pinctrl",
	"/sys/kernel/debug/clock", "/sys/kernel/debug/clk", "/sys/kernel/debug/regulator",
}

// collectSystem stores /proc, the boot log, sysfs and debugfs attributes,
// and the /dev nodes.
// TRLC-LINKS: REQ-SDS-284
func collectSystem(a *Archive) {
	for _, f := range procFiles {
		if _, err := os.Stat("/proc/" + f); err == nil {
			a.AddFile("system/proc/"+f, "/proc/"+f, 4<<20)
		}
	}
	for _, cmd := range [][]string{{"dmesg"}, {"uname", "-a"}, {"df"}, {"ifconfig", "-a"}, {"route", "-n"}, {"netstat", "-ltnu"}, {"lsmod"}, {"busybox"}} {
		a.Add("system/cmd/"+strings.Join(cmd, "_")+".txt", run(cmd...), "", "")
	}
	walkAttrs(a, "sys", sysRoots, 4, 4096, 8<<20)
	walkAttrs(a, "sysdev", []string{sysDevices}, 8, 4096, 16<<20)
	walkAttrs(a, "debug", debugRoots, 3, 64<<10, 4<<20)
	a.AddText("system/dev.txt", devNodes("/dev"))
	a.AddText("system/lib_modules.txt", listTree("/lib/modules", 4))
}

// run runs a busybox command for at most 20 s and returns its output and,
// on failure, the error.
// TRLC-LINKS: REQ-SDS-284
func run(argv ...string) []byte {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, argv[0], argv[1:]...).CombinedOutput() //nolint:gosec // G204: the collectors' own fixed commands
	if err != nil {
		out = append(out, []byte(fmt.Sprintf("\n[%v]\n", err))...)
	}
	return out
}

// walkAttrs stores the readable regular files under roots, at most depth
// levels deep and size bytes each, to at most total bytes. Links are not
// followed: sysfs links back to its own trees.
// TRLC-LINKS: REQ-SDS-284
func walkAttrs(a *Archive, prefix string, roots []string, depth int, size, total int64) {
	var stored int64
	var b strings.Builder
	for _, root := range roots {
		base := strings.Count(root, "/")
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil //nolint:nilerr // an unreadable branch is left out; the walk goes on
			}
			if d.IsDir() && strings.Count(p, "/")-base >= depth {
				return filepath.SkipDir
			}
			if d.Type()&fs.ModeSymlink != 0 {
				t, _ := os.Readlink(p)
				fmt.Fprintf(&b, "%s -> %s\n", p, t)
				return nil
			}
			if !d.Type().IsRegular() || stored > total {
				return nil
			}
			info, err := d.Info()
			if err != nil || info.Mode().Perm()&0o444 == 0 {
				return nil //nolint:nilerr // not readable: listed by its name only
			}
			data, err := readSome(p, size)
			if err != nil {
				fmt.Fprintf(&b, "%s [%v]\n", p, err)
				return nil
			}
			stored += int64(len(data))
			a.Add(prefix+p, data, p, "")
			return nil
		})
	}
	a.AddText(prefix+"/links.txt", b.String())
}

// readSome reads at most limit bytes of path, waiting at most 2 s: a few
// sysfs attributes block.
// TRLC-LINKS: REQ-SDS-284
func readSome(path string, limit int64) ([]byte, error) {
	type res struct {
		b   []byte
		err error
	}
	ch := make(chan res, 1)
	go func() {
		f, err := os.Open(path) //nolint:gosec // G304: a sysfs or debugfs attribute the walk found
		if err != nil {
			ch <- res{nil, err}
			return
		}
		defer func() { _ = f.Close() }()
		b := make([]byte, limit)
		n, err := f.Read(b)
		if n > 0 {
			err = nil
		}
		ch <- res{b[:max(n, 0)], err}
	}()
	select {
	case r := <-ch:
		return r.b, r.err
	case <-time.After(2 * time.Second):
		return nil, errors.New("read blocked")
	}
}

// devNodes lists the device nodes under dir with their major and minor numbers.
// TRLC-LINKS: REQ-SDS-284
func devNodes(dir string) string {
	var lines []string
	_ = filepath.WalkDir(dir, func(p string, _ fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // an unreadable branch is left out
		}
		info, err := os.Lstat(p)
		if err != nil {
			return nil //nolint:nilerr // gone since the walk listed it
		}
		st, _ := info.Sys().(*syscall.Stat_t)
		line := fmt.Sprintf("%-40s %s", p, info.Mode())
		if st != nil && info.Mode()&(fs.ModeDevice|fs.ModeCharDevice) != 0 {
			line += fmt.Sprintf(" %d,%d", (st.Rdev>>8)&0xfff, st.Rdev&0xff|(st.Rdev>>12)&^0xff)
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			t, _ := os.Readlink(p)
			line += " -> " + t
		}
		lines = append(lines, line)
		return nil
	})
	sort.Strings(lines)
	return strings.Join(lines, "\n") + "\n"
}

// listTree lists the files under dir to depth levels with their sizes.
// TRLC-LINKS: REQ-SDS-284
func listTree(dir string, depth int) string {
	var b strings.Builder
	base := strings.Count(dir, "/")
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // an unreadable branch is left out
		}
		if d.IsDir() && strings.Count(p, "/")-base >= depth {
			return filepath.SkipDir
		}
		if info, err := d.Info(); err == nil {
			fmt.Fprintf(&b, "%s %d %s\n", info.Mode(), info.Size(), p)
		}
		return nil
	})
	return b.String()
}
