package lcd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// Bringup makes /dev/fb0 exist and the panel lit (spec 07 §1.1). On this
// unit the boot firmware normally loads the LCD stack already, so each step
// is skipped when its effect is present. Module load order is load-bearing:
// da8xx-fb depends on the three cfb* helpers and must load LAST.
func Bringup(logf func(string, ...any)) error {
	if _, err := os.Stat("/dev/fb0"); err != nil {
		for _, mod := range []string{"cfbcopyarea.ko", "cfbfillrect.ko", "cfbimgblt.ko", "da8xx-fb.ko"} {
			path := findModule(mod)
			if path == "" {
				logf("lcd: module %s not found", mod)
				continue
			}
			if out, err := exec.Command("insmod", path).CombinedOutput(); err != nil {
				logf("lcd: insmod %s: %v %s", mod, err, out)
			}
		}
		if _, err := os.Stat("/dev/fb0"); err != nil {
			return fmt.Errorf("lcd: /dev/fb0 unavailable: %w", err)
		}
	}

	// GPIO7 = panel enable (binary; no brightness control exists). Ignore
	// errors: on a warm system the panel is already lit.
	if _, err := os.Stat("/sys/class/gpio/gpio7"); err != nil {
		os.WriteFile("/sys/class/gpio/export", []byte("7"), 0o644)
	}
	os.WriteFile("/sys/class/gpio/gpio7/direction", []byte("out"), 0o644)
	os.WriteFile("/sys/class/gpio/gpio7/value", []byte("1"), 0o644)
	return nil
}

func findModule(name string) string {
	for _, dir := range []string{"/lib/modules", "/usr/bin/siglent/modules", "/usr/bin/siglent"} {
		var found string
		filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if info != nil && !info.IsDir() && info.Name() == name {
				found = p
				return filepath.SkipAll
			}
			return nil
		})
		if found != "" {
			return found
		}
	}
	return ""
}
