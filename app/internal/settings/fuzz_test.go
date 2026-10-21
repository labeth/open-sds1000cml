// ENGMODEL-OWNER-UNIT: FU-APP-SETTINGS
package settings_test

import (
	"testing"
	"time"

	"open-sds/app/internal/analog"
	"open-sds/app/internal/analysis"
	"open-sds/app/internal/engine"
	"open-sds/app/internal/panel"
	"open-sds/app/internal/panel/framecalc"
	"open-sds/app/internal/settings"
)

// FuzzParseAndApply hammers the loader with hostile bytes: Parse must never
// panic, and anything it does accept must Apply cleanly through the real
// owners (engine staging setters, front-end offset law, controller clamps) —
// a malformed settings file must NEVER be able to take the boot down.
// TRLC-LINKS: REQ-SDS-072
func FuzzParseAndApply(f *testing.F) {
	f.Add([]byte(`{"version":1,"tdiv_s":0.0005,"ch":[{"vdiv_v":0.5,"offset_v":1.5,"offset_set":true,"coupling":1,"probe":10},{"vdiv_v":2}],"vert_set":true,"trigger":{"level_code":30500,"rising":true,"source":1,"type":2,"norm":true,"holdoff_s":0.001},"acq":{"mode":1,"avg_count":64,"eres_len":1},"decode":{"proto":4,"baud":9600,"cha":1,"chb":0,"cpol":true,"cpha":true,"format":1},"view_mode":4}`))
	f.Add([]byte(`{"version":1}`))
	f.Add([]byte(`{"version":1,"trigger":{"level_code":-1},"acq":{"mode":-1},"view_mode":-1}`))
	f.Add([]byte(`{"version":1,"tdiv_s":1e308,"ch":[{"vdiv_v":-1e308,"offset_v":1e308,"offset_set":true}],"vert_set":true}`))
	f.Add([]byte(`{"version":2}`))
	f.Add([]byte(`null`))
	f.Add([]byte(`[[[[[`))
	f.Add([]byte("\x00\xff\xfe"))
	// The bus layers, the decoded stream's trigger and SOUND.
	f.Add([]byte(`{"version":1,"decode":{"proto":2,"layer":"eeprom24xx","layer_addr":81,"layer_dir":2,"stream_trig":1,"stream_value":165},"sound":{"set":true,"a4":432,"ch":1,"ref":"euro","range":"bass","slow":true,"page":3,"cycles":4,"ab_in":1,"env_cv":true,"gate":1,"gate_v":2.5,"voct_auto":true,"drift":true}}`))
	f.Add([]byte(`{"version":1,"decode":{"proto":37,"layer":"no-such","layer_addr":-9,"layer_dir":99,"stream_trig":-4,"stream_value":-1},"sound":{"set":true,"a4":-1e308,"ch":-3,"ref":"x","range":"loud","page":-7,"cycles":3,"ab_in":9,"gate":-9,"gate_v":1e308,"pre_s":-5,"post_s":1e308}}`))
	f.Add([]byte(`{"version":1,"decode":{"proto":52,"frame_bits":9999,"layer":"usb_request"},"acq":{"roll_stream":true},"sound":{"set":true,"page":2147483647,"cycles":-2147483648}}`))

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
		// (a decoder of the BUS tab's: framecalc's protocols, Auto loading as
		// a detection, never itself)
		if v.ViewMode < 0 || v.ViewMode > 5 || v.Decode.Proto < 0 || v.Decode.Proto >= framecalc.NumProtos || v.Decode.Proto == framecalc.ProtoAuto ||
			v.Decode.ChA>>1 != 0 || v.Decode.ChB>>1 != 0 || v.Decode.Format < 0 || v.Decode.Format > 2 {
			t.Fatalf("hostile input escaped the domain clamps: %+v", v)
		}
		if d := v.Decode; d.LayerDir < 0 || d.LayerDir > 2 || d.LayerAddr < 0 || d.LayerAddr > 128 || d.StreamTrig < 0 || d.StreamTrig > 2 {
			t.Fatalf("hostile layer or stream settings escaped: %+v", d)
		}
		if _, ok := analysis.LayerByID(v.Decode.Layer); v.Decode.Layer != "" && !ok {
			t.Fatalf("an unknown layer %q was taken", v.Decode.Layer)
		}
		if so := v.Sound; !(so.A4 >= 400 && so.A4 <= 480) || so.Ch>>1 != 0 || so.ABIn>>1 != 0 || so.Page < 0 || so.Page > 4 ||
			(so.Cycles != 1 && so.Cycles != 2 && so.Cycles != 4) || so.Gate < -1 || so.Gate > 1 || !(so.GateV >= -50 && so.GateV <= 50) ||
			!(so.PreS >= 0 && so.PostS >= 0.1 && so.PreS+so.PostS <= 16.0001) {
			t.Fatalf("hostile SOUND settings escaped: %+v", so)
		}
	})
}
