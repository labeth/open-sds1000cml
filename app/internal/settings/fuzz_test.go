package settings_test

import (
	"testing"
	"time"

	"open-sds/app/internal/analog"
	"open-sds/app/internal/engine"
	"open-sds/app/internal/panel"
	"open-sds/app/internal/settings"
)

// FuzzParseAndApply hammers the loader with hostile bytes: Parse must never
// panic, and anything it does accept must Apply cleanly through the real
// owners (engine staging setters, front-end offset law, controller clamps) —
// a malformed settings file must NEVER be able to take the boot down.
func FuzzParseAndApply(f *testing.F) {
	f.Add([]byte(`{"version":1,"tdiv_s":0.0005,"ch":[{"vdiv_v":0.5,"offset_v":1.5,"offset_set":true,"coupling":1,"probe":10},{"vdiv_v":2}],"vert_set":true,"trigger":{"level_code":30500,"rising":true,"source":1,"type":2,"norm":true,"holdoff_s":0.001},"acq":{"mode":1,"avg_count":64,"eres_len":1},"decode":{"proto":4,"baud":9600,"cha":1,"chb":0,"cpol":true,"cpha":true,"format":1},"view_mode":4}`))
	f.Add([]byte(`{"version":1}`))
	f.Add([]byte(`{"version":1,"trigger":{"level_code":-1},"acq":{"mode":-1},"view_mode":-1}`))
	f.Add([]byte(`{"version":1,"tdiv_s":1e308,"ch":[{"vdiv_v":-1e308,"offset_v":1e308,"offset_set":true}],"vert_set":true}`))
	f.Add([]byte(`{"version":2}`))
	f.Add([]byte(`null`))
	f.Add([]byte(`[[[[[`))
	f.Add([]byte("\x00\xff\xfe"))

	// One shared rig: engine/front-end/controller state pollution across runs
	// is irrelevant for panic-hunting, and rebuilding the arena per input
	// would dominate the fuzz loop.
	e := engine.New(engine.Config{Bus: nullBus{}, Logf: func(string, ...any) {}})
	fe := analog.New(&nullSPI{}, func(time.Duration) {}, nil)
	fe.OnOffset(e.SetOffsetDAC)
	fe.OnVdiv(e.SetChannelVdiv)
	pc := panel.New(e, fe, -1, engine.SupportedTdivs(), 500e-6, func(string, ...any) {})

	f.Fuzz(func(t *testing.T, raw []byte) {
		s, err := settings.Parse(raw)
		if err != nil {
			return // rejected → boot falls back to defaults, by design
		}
		settings.Apply(s, e, fe, pc, nil)
		// The applied controller state must land inside the menu domains no
		// matter what the file said.
		v := pc.SettingsView()
		if v.ViewMode < 0 || v.ViewMode > 4 || v.Decode.Proto < 0 || v.Decode.Proto > 4 ||
			v.Decode.ChA>>1 != 0 || v.Decode.ChB>>1 != 0 || v.Decode.Format < 0 || v.Decode.Format > 2 {
			t.Fatalf("hostile input escaped the domain clamps: %+v", v)
		}
	})
}
