// ENGMODEL-OWNER-UNIT: FU-APP-LCD

package lcd

import "os"

// SetBacklight switches the panel's light through GPIO 7, the display and
// backlight enable (spec 08 §10.1): the only light control this LCD has.
// TRLC-LINKS: REQ-SDS-221
func SetBacklight(on bool) error {
	v := []byte("0")
	if on {
		v = []byte("1")
	}
	return os.WriteFile("/sys/class/gpio/gpio7/value", v, 0o600)
}
