// ENGMODEL-OWNER-UNIT: FU-OTA-DIAG

package diag

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	core "github.com/labeth/open-dso-core/supervisor/diag"
	"github.com/labeth/open-dso-core/supervisor/fdinherit"
)

// The probe image's control word (register 0x19): hold D1, D2, G2 and A11 at
// the acquisition images' levels; drive the panel's load and clock low and
// F1 high; scan the panel chain once; pulse F1 low.
const (
	prCtl       = 0x19
	ctlOthers   = 1 << 0
	ctlPanel    = 1 << 1
	ctlScan     = 1 << 2
	ctlPulseF1  = 1 << 3
	prSRAMStart = 0x1a
	stScan      = 1 << 2
	stSRAMRun   = 1 << 3
	stSRAMDone  = 1 << 4
)

// SRAMResult is the probe image's SRAM test: every word written and read
// back through the address counter.
type SRAMResult struct {
	Checked      uint32 `json:"words_checked"`
	PatternError uint32 `json:"words_breaking_the_pattern"`
	CountError   uint32 `json:"words_not_the_one_before_plus_one"`
	BadBits      uint32 `json:"data_bits_ever_wrong"`
	First        uint32 `json:"first_word"`
	Error        string `json:"error,omitempty"`
}

// sramTest runs the probe image's SRAM test and reads its counts.
// TRLC-LINKS: REQ-SDS-291
func (p *probe) sramTest() SRAMResult {
	var r SRAMResult
	if err := p.b.Write(planeCS1, prSRAMStart, 1); err != nil {
		r.Error = err.Error()
		return r
	}
	p.sleep(50 * time.Millisecond)
	done := false
	for range 200 {
		v, err := p.b.Read(planeCS1, prStatus)
		if err == nil && v&stSRAMRun == 0 && v&stSRAMDone != 0 {
			done = true
			break
		}
		p.sleep(5 * time.Millisecond)
	}
	if !done {
		r.Error = "the SRAM test did not end"
		return r
	}
	read32 := func(lo uint16, hiBits int) uint32 {
		a, _ := p.b.Read(planeCS1, lo)
		b, _ := p.b.Read(planeCS1, lo+1)
		return uint32(a) | uint32(b&(1<<hiBits-1))<<16
	}
	r.PatternError, r.CountError = read32(0x48, 8), read32(0x4a, 8)
	r.BadBits, r.Checked = read32(0x4c, 16), read32(0x4e, 8)
	lo, _ := p.b.Read(planeCS1, 0x1b)
	hi, _ := p.b.Read(planeCS1, 0x1c)
	r.First = uint32(lo) | uint32(hi)<<16
	return r
}

// PanelResult is the key chain's scan and the key interrupt.
type PanelResult struct {
	Chain    string `json:"chain"` // 64 bits, bit 0 first (0 = pressed or phase low)
	IRQ      bool   `json:"key_interrupt_seen"`
	IRQQuiet bool   `json:"no_interrupt_without_the_pulse"`
	Error    string `json:"error,omitempty"`
}

// panelTests scans the key chain and pulses the key interrupt line, with the
// panel's lines driven as the images drive them, and watches for SIGIO on
// the inherited /dev/fpga_key descriptor, as the app reads its keys.
// TRLC-LINKS: REQ-SDS-291
func (p *probe) panelTests() PanelResult {
	var r PanelResult
	ctl := uint16(ctlOthers | ctlPanel)
	if err := p.b.Write(planeCS1, prCtl, ctl); err != nil {
		r.Error = err.Error()
		return r
	}
	p.sleep(5 * time.Millisecond)
	if err := p.b.Write(planeCS1, prCtl, ctl|ctlScan); err != nil {
		r.Error = err.Error()
		return r
	}
	for range 100 {
		p.sleep(time.Millisecond)
		if v, err := p.b.Read(planeCS1, prStatus); err == nil && v&stScan == 0 {
			break
		}
	}
	var bits strings.Builder
	for w := range 4 {
		v, err := p.b.Read(planeCS1, 0x0c+uint16(w))
		if err != nil {
			r.Error = err.Error()
			return r
		}
		for i := range 16 {
			bits.WriteByte('0' + byte(v>>i&1))
		}
	}
	r.Chain = bits.String()
	fd := fdinherit.Find("/dev/fpga_key")
	if fd < 0 {
		r.Error = "no inherited /dev/fpga_key"
		return r
	}
	sig := make(chan os.Signal, 4)
	signal.Notify(sig, syscall.SIGIO)
	defer signal.Stop(sig)
	flags, err := armAsync(fd)
	if err != nil {
		r.Error = err.Error()
		return r
	}
	defer func() { _, _, _ = syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), syscall.F_SETFL, flags) }()
	drain := func() {
		for len(sig) > 0 {
			<-sig
		}
	}
	p.sleep(20 * time.Millisecond)
	drain()
	select {
	case <-sig:
	case <-time.After(200 * time.Millisecond):
		r.IRQQuiet = true
	}
	drain()
	if err := p.b.Write(planeCS1, prCtl, ctl|ctlPulseF1); err != nil {
		r.Error = err.Error()
		return r
	}
	select {
	case <-sig:
		r.IRQ = true
	case <-time.After(200 * time.Millisecond):
	}
	return r
}

// armAsync makes fd signal this process (SIGIO) on input; it returns the
// flags to put back.
// TRLC-LINKS: REQ-SDS-291
func armAsync(fd int) (uintptr, error) {
	if _, _, errno := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), syscall.F_SETOWN, uintptr(syscall.Getpid())); errno != 0 {
		return 0, errno
	}
	flags, _, errno := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), syscall.F_GETFL, 0)
	if errno != 0 {
		return 0, errno
	}
	if _, _, errno := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), syscall.F_SETFL, flags|syscall.O_ASYNC); errno != 0 {
		return 0, errno
	}
	return flags, nil
}

// OutputResult is which FPGA ball one of the ARM's outputs reaches.
type OutputResult struct {
	Ball  string `json:"ball,omitempty"`
	Error string `json:"error,omitempty"`
}

// writeSys writes value to a sysfs file.
// TRLC-LINKS: REQ-SDS-291
func writeSys(path, value string) error {
	return os.WriteFile(path, []byte(value), 0o200)
}

// armOutputs finds the FPGA balls the ARM's gpio113 (spec 08 §10.2) and its
// eHRPWM channel (10 kHz, spec 08 §10.3) reach, if any: the ball that follows
// gpio113 between two gates, and one that toggles only while the PWM runs.
// TRLC-LINKS: REQ-SDS-291
func (p *probe) armOutputs(balls []Ball) (gpio, pwm OutputResult) {
	candidate := func(i int) bool { g := balls[i].Group; return g != "adc" && g != "clock" }
	g := "/sys/class/gpio/gpio113"
	if _, err := os.Stat(g); err != nil {
		_ = writeSys("/sys/class/gpio/export", "113")
	}
	if err := writeSys(g+"/direction", "out"); err != nil {
		gpio.Error = err.Error()
	} else {
		// a ball that follows gpio113 through 0, 1, 0, 1 and never toggles
		// within a gate (the trigger comparator toggles on the square)
		follows := make([]bool, len(balls))
		for i := range follows {
			follows[i] = candidate(i)
		}
		for _, v := range []string{"0", "1", "0", "1"} {
			_ = writeSys(g+"/value", v)
			p.sleep(2 * time.Millisecond)
			_ = p.gate(probeClockHz / 1000)
			e0, e1, lv, err := p.flags()
			if err != nil {
				gpio.Error = err.Error()
				break
			}
			for i := range follows {
				follows[i] = follows[i] && (!e0[i] || !e1[i]) && lv[i] == (v == "1")
			}
		}
		for i, f := range follows {
			if f && gpio.Error == "" {
				gpio.Ball = balls[i].Ball
			}
		}
		if gpio.Ball == "" && gpio.Error == "" {
			gpio.Error = "no ball followed gpio113"
		}
	}
	ehrDir := "/sys/devices/platform/omap/ehrpwm.0/pwm/ehrpwm.0:0"
	toggling := func() []bool {
		_ = p.gate(probeClockHz / 100)
		e0, e1, _, _ := p.flags()
		out := make([]bool, len(balls))
		for i := range out {
			out[i] = e0 != nil && e0[i] && e1[i]
		}
		return out
	}
	if _, err := os.Stat(ehrDir); err != nil {
		pwm.Error = err.Error()
		return gpio, pwm
	}
	_ = writeSys(ehrDir+"/request", "1")
	_ = writeSys(ehrDir+"/period_freq", "10000")
	_ = writeSys(ehrDir+"/duty_percent", "50")
	before := toggling()
	if err := writeSys(ehrDir+"/run", "1"); err != nil {
		pwm.Error = err.Error()
		return gpio, pwm
	}
	p.sleep(5 * time.Millisecond)
	during := toggling()
	_ = writeSys(ehrDir+"/run", "0")
	for i := range balls {
		if candidate(i) && during[i] && !before[i] {
			pwm.Ball = balls[i].Ball
		}
	}
	if pwm.Ball == "" {
		pwm.Error = "no ball toggled with the 10 kHz PWM"
	}
	return gpio, pwm
}

// ledWalk lights each panel LED in turn through the CS3 latch (spec 08 §8.1),
// then all of them, then none: nothing reads them back, so the walk is for
// the person at the scope to see.
// TRLC-LINKS: REQ-SDS-291
func ledWalk(b regBus, sleep func(time.Duration)) error {
	set := func(w uint16) error {
		for _, s := range [][2]uint16{{0x0b, 0}, {0x0a, w >> 8}, {0x09, w & 0xff}, {0x0b, 1}} {
			if err := b.Write(planeCS3, s[0], s[1]); err != nil {
				return err
			}
		}
		return nil
	}
	for bit := range 16 {
		if err := set(1 << bit); err != nil {
			return err
		}
		sleep(250 * time.Millisecond)
	}
	if err := set(0xffff); err != nil {
		return err
	}
	sleep(time.Second)
	return set(0)
}

// armInventory is the ARM side's devices, one "kind name" a line: network
// interfaces and PHYs, USB devices but mass storage (the stick), I2C and SPI
// devices, platform devices with their drivers, MTD partitions, input
// devices, sound cards, framebuffers, RTCs, MMC cards, GPIO chips and the
// watchdog.
// TRLC-LINKS: REQ-SDS-291
func armInventory(root string) []string {
	var out []string
	add := func(f string, args ...any) { out = append(out, fmt.Sprintf(f, args...)) }
	rd := func(p string) string { return strings.TrimSpace(core.ReadStr(filepath.Join(root, p))) }
	each := func(dir string, fn func(name, path string)) {
		ents, _ := os.ReadDir(filepath.Join(root, dir))
		for _, e := range ents {
			fn(e.Name(), filepath.Join(dir, e.Name()))
		}
	}
	each("sys/class/net", func(n, _ string) {
		if n != "lo" {
			add("net %s", n)
		}
	})
	each("sys/bus/mdio_bus/devices", func(n, p string) { add("phy %s %s", n, rd(p+"/phy_id")) })
	each("sys/bus/usb/devices", func(_, p string) {
		vid := rd(p + "/idVendor")
		if vid == "" {
			return
		}
		storage := false
		each(p, func(_, ip string) {
			if rd(ip+"/bInterfaceClass") == "08" {
				storage = true
			}
		})
		if !storage {
			add("usb %s:%s %s", vid, rd(p+"/idProduct"), rd(p+"/product"))
		}
	})
	each("sys/bus/i2c/devices", func(n, p string) {
		// a board file names its I2C devices whether or not they are fitted;
		// a driver that bound one found it
		bound := "unbound"
		if drv, err := os.Readlink(filepath.Join(root, p, "driver")); err == nil {
			bound = "bound " + filepath.Base(drv)
		}
		add("i2c %s %s %s", n, rd(p+"/name"), bound)
	})
	each("sys/bus/spi/devices", func(n, p string) { add("spi %s %s", n, rd(p+"/modalias")) })
	each("sys/bus/platform/devices", func(n, p string) {
		drv, _ := os.Readlink(filepath.Join(root, p, "driver"))
		add("platform %s %s", n, filepath.Base(drv))
	})
	for _, l := range strings.Split(core.ReadStr(filepath.Join(root, "proc/mtd")), "\n")[1:] {
		if l = strings.TrimSpace(l); l != "" {
			add("mtd %s", l)
		}
	}
	for l := range strings.SplitSeq(core.ReadStr(filepath.Join(root, "proc/bus/input/devices")), "\n") {
		if n, ok := strings.CutPrefix(l, "N: Name="); ok {
			add("input %s", n)
		}
	}
	for l := range strings.SplitSeq(core.ReadStr(filepath.Join(root, "proc/asound/cards")), "\n") {
		if strings.Contains(l, "]:") {
			add("sound %s", strings.TrimSpace(l))
		}
	}
	each("sys/class/graphics", func(n, p string) {
		if strings.HasPrefix(n, "fb") {
			add("fb %s %s", n, rd(p+"/name"))
		}
	})
	each("sys/class/rtc", func(n, p string) { add("rtc %s %s", n, rd(p+"/name")) })
	each("sys/bus/mmc/devices", func(n, p string) { add("mmc %s %s", n, rd(p+"/type")) })
	each("sys/class/gpio", func(n, p string) {
		if strings.HasPrefix(n, "gpiochip") {
			add("gpio %s %s", n, rd(p+"/label"))
		}
	})
	if _, err := os.Stat(filepath.Join(root, "dev/watchdog")); err == nil {
		add("watchdog /dev/watchdog")
	}
	sort.Strings(out)
	return out
}

// i2cScan reads one byte from every address of every I2C bus that no driver
// holds, as i2cdetect -r does, and lists the addresses that answer and those
// a driver holds ("i2c-1 0x50 answers", "i2c-1 0x2d held").
// TRLC-LINKS: REQ-SDS-291
func i2cScan() []string {
	const i2cSlave = 0x0703
	var out []string
	buses, _ := filepath.Glob("/dev/i2c-*")
	sort.Strings(buses)
	for _, bus := range buses {
		fd, err := syscall.Open(bus, syscall.O_RDWR|syscall.O_CLOEXEC, 0)
		if err != nil {
			continue
		}
		name := filepath.Base(bus)
		for addr := 0x03; addr <= 0x77; addr++ {
			if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), i2cSlave, uintptr(addr)); errno != 0 {
				if errno == syscall.EBUSY {
					out = append(out, fmt.Sprintf("%s 0x%02x held", name, addr))
				}
				continue
			}
			var b [1]byte
			if n, err := syscall.Read(fd, b[:]); err == nil && n == 1 {
				out = append(out, fmt.Sprintf("%s 0x%02x answers", name, addr))
			}
		}
		_ = syscall.Close(fd)
	}
	return out
}
