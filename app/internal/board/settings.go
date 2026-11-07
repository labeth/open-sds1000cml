// ENGMODEL-OWNER-UNIT: FU-APP-SETTINGS

package board

import settings "github.com/labeth/open-dso-core/settings"

// SettingsExtension is the board's part of the settings file: the trigger
// level as the comparator DAC code (level_code), as the file has always
// kept it.
type SettingsExtension struct{ Levels *Levels }

var _ settings.Extension = SettingsExtension{}

// CollectTrigger writes the comparator's code into t; 0 is the boot
// comparator, untouched.
// TRLC-LINKS: REQ-SDS-089
func (x SettingsExtension) CollectTrigger(t *settings.Trigger) {
	t.LevelCode = int(x.Levels.eng.Snapshot().TrigCode)
}

// ApplyTrigger stages t's code. Code 0 (the boot comparator, untouched) is
// never staged; the range is checked in integers, as an out-of-range
// float-to-uint16 conversion is implementation-defined on ARMv7.
// TRLC-LINKS: REQ-SDS-072
func (x SettingsExtension) ApplyTrigger(t settings.Trigger) {
	if c := t.LevelCode; c > 0 && c <= 0xFFFF {
		x.Levels.eng.SetTrigLevelCode(uint16(c)) // clamps to the operational window
	}
}
