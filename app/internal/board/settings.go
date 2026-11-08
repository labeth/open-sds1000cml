// ENGMODEL-OWNER-UNIT: FU-APP-SETTINGS

package board

import settings "github.com/labeth/open-dso-core/settings"

// SettingsExtension is the board's part of the settings file: the trigger
// level as the comparator DAC code (level_code), as the file has always
// kept it.
type SettingsExtension struct{ Levels *Levels }

var _ settings.Extension = SettingsExtension{}

// CollectTrigger writes the instrument model's level into t as the
// comparator's code (the volts the model holds are that code's, so the
// code comes back exactly); 0 is the boot comparator, untouched. Without a
// model it is the code staged.
// TRLC-LINKS: REQ-SDS-089
func (x SettingsExtension) CollectTrigger(t *settings.Trigger) {
	l := x.Levels
	if l.m == nil {
		t.LevelCode = int(l.eng.Snapshot().TrigCode)
		return
	}
	s := l.m.State()
	t.LevelCode = 0
	if s.LevelSet {
		t.LevelCode = int(clampCode(l.trigCodeOf(s.Trigger.LevelV(), s.Trigger.Source().Index)))
	}
}

// ApplyTrigger stages t's code. Code 0 (the boot comparator, untouched) is
// never staged; the range is checked in integers, as an out-of-range
// float-to-uint16 conversion is implementation-defined on ARMv7.
// TRLC-LINKS: REQ-SDS-072
func (x SettingsExtension) ApplyTrigger(t settings.Trigger) {
	if c := t.LevelCode; c > 0 && c <= 0xFFFF {
		x.Levels.SetTrigLevelCode(uint16(c)) // clamps to the operational window
	}
}
