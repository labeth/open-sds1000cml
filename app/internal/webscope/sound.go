// ENGMODEL-OWNER-UNIT: FU-APP-WEB

package webscope

import (
	"github.com/labeth/open-dso-core/scopeweb"
	"github.com/labeth/open-dso-core/uimodel/panel"
)

// SOUND (ADR-SOUND) on the scope's side of the web server: the stream,
// its takes and the arbitration between the LCD and the pages are the web
// scope's over the engine and the panel; the settings, actions and
// readouts here are the panel's SOUND service, whose report is the panel's
// own type until the UI model moves to the core.

// soundPanel is the panel's SOUND service (panel/sound.go).
type soundPanel interface {
	SoundSettings() panel.SoundSettings
	SetSoundSettings(panel.SoundSettings) panel.SoundSettings
	SoundAction(string) error
	SoundReport(api, marks bool) panel.SoundReport
	SoundWant(api, marks bool)
}

// soundControls is the panel's SOUND service.
type soundControls struct {
	s  *Scope
	sp soundPanel
}

// TRLC-LINKS: REQ-SDS-274
func (c soundControls) SoundSettings() panel.SoundSettings { return c.sp.SoundSettings() }

// TRLC-LINKS: REQ-SDS-274
func (c soundControls) SetSoundSettings(v panel.SoundSettings) panel.SoundSettings {
	return c.sp.SetSoundSettings(v)
}

// TRLC-LINKS: REQ-SDS-274
func (c soundControls) SoundAction(name string) error { return c.sp.SoundAction(name) }

// SoundReport is the panel's report with the stream's state now (the
// service reads it only while it runs).
// TRLC-LINKS: REQ-SDS-274
func (c soundControls) SoundReport(api, marks bool) any {
	rep := c.sp.SoundReport(api, marks)
	if src, ok := c.s.sc.(scopeweb.SoundStream); ok {
		rep.Info = src.SoundInfo()
	}
	return rep
}

// TRLC-LINKS: REQ-SDS-274
func (c soundControls) SoundWant(api, marks bool) { c.sp.SoundWant(api, marks) }
