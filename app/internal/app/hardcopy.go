// ENGMODEL-OWNER-UNIT: FU-APP-APP

package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// uDisk is the USB stick's mount point (PRINT saves screenshots there).
const uDisk = "/usr/bin/siglent/usr/media/U-disk0"

// mountedAt reports a filesystem mounted at dir.
// TRLC-LINKS: REQ-SDS-136
func mountedAt(dir string) bool {
	mounts, err := os.ReadFile("/proc/mounts")
	return err == nil && strings.Contains(string(mounts), " "+dir+" ")
}

// saveHardcopy writes png as the next free SDSnnnn.png in dir and returns
// its name.
// TRLC-LINKS: REQ-SDS-136
func saveHardcopy(dir string, png []byte) (string, error) {
	for n := 1; n < 10000; n++ {
		name := fmt.Sprintf("SDS%04d.png", n)
		f, err := os.OpenFile(filepath.Join(dir, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644) //nolint:gosec // G302: a screenshot the owner copies off the stick
		if os.IsExist(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		_, werr := f.Write(png)
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		return name, werr
	}
	return "", fmt.Errorf("%s: no free SDSnnnn.png name", dir)
}
