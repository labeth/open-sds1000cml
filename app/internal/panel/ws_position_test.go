// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"

	"open-sds/app/internal/panel/wsview"
)

// toolText is the scope's tool line as "Knob:job" words.
// TRLC-LINKS: REQ-SDS-213
func toolText(c *Controller) string {
	var s []string
	for _, sg := range c.PanelStatus().Tool {
		s = append(s, sg.Knob+":"+sg.Job)
	}
	return strings.Join(s, " ")
}

// Horizontal Position moves the trigger position in every workspace (and
// view); where a workspace has a second job its push switches Position to
// it and back, the tool line saying which; SCOPE and COMPARE keep Zero.
// TRLC-LINKS: REQ-SDS-213
func TestPositionMovesTheTriggerInEveryWorkspace(t *testing.T) {
	r := newViewRig(t)
	c := r.c
	uart := func() { r.show(uartFrame([]string{"AB\r\n", "Hi!\r\n", "OK\r\n"}, 460800, 100e-6, 100e-9, 20000)) }
	uart()
	type place struct {
		name, keys, job string // job: the second Position job ("" none)
	}
	for _, p := range []place{
		{"SCOPE", "f1", ""},
		{"BUS packets", "f2", "next match"},
		{"BUS eye", "f2", "sample point"},
		{"SPECTRUM FFT", "f3", "fine marker"},
		{"SPECTRUM spectrogram", "f3", "fine marker"},
		{"SPECTRUM Bode", "f3", "point"},
		{"MEASURE", "f4", "cursor B"},
		{"COMPARE", "f5", ""},
	} {
		press(t, c, p.keys)
		uart()
		r.eng.SetTrigPosFrac(0.5)
		if strings.HasPrefix(p.name, "SPECTRUM") && p.name != "SPECTRUM FFT" {
			// F3 again stepped the view: check it is the one named
			want := map[string]int{"SPECTRUM spectrogram": viewSpectrogram, "SPECTRUM Bode": viewBode}[p.name]
			if c.snap().viewMode != want {
				t.Fatalf("%s: view %d", p.name, c.snap().viewMode)
			}
		}
		if c.ui.posJob {
			t.Fatalf("%s: Position starts on its second job", p.name)
		}
		if tl := toolText(c); !strings.Contains(tl, "Position:trigger position") {
			t.Fatalf("%s: tool line %q", p.name, tl)
		}
		turn(c, "horizpos", 5)
		if f := r.eng.Snapshot().TrigPosFrac; math.Abs(f-0.55) > 1e-9 {
			t.Fatalf("%s: Position turned the trigger to %g, want 0.55", p.name, f)
		}
		if p.job == "" {
			if tl := toolText(c); !strings.Contains(tl, "Position:trigger position push:centre") {
				t.Fatalf("%s: tool line %q", p.name, tl)
			}
			press(t, c, "horizpospush")
			if f := r.eng.Snapshot().TrigPosFrac; f != 0.5 {
				t.Fatalf("%s: Zero left the trigger at %g", p.name, f)
			}
			continue
		}
		if tl := toolText(c); !strings.Contains(tl, "Position:trigger position push:"+p.job) {
			t.Fatalf("%s: tool line %q", p.name, tl)
		}
		press(t, c, "horizpospush")
		if tl := toolText(c); !c.ui.posJob || !strings.Contains(tl, "Position:"+p.job+" push:trigger position") ||
			!strings.Contains(c.View().Notice, "Position: "+p.job) {
			t.Fatalf("%s: push: second job %v, tool line %q, notice %q", p.name, c.ui.posJob, tl, c.View().Notice)
		}
		turn(c, "horizpos", 2)
		if f := r.eng.Snapshot().TrigPosFrac; math.Abs(f-0.55) > 1e-9 {
			t.Fatalf("%s: the second job moved the trigger to %g", p.name, f)
		}
		press(t, c, "horizpospush")
		if tl := toolText(c); c.ui.posJob || !strings.Contains(tl, "Position:trigger position push:"+p.job) {
			t.Fatalf("%s: push again: second job %v, tool line %q", p.name, c.ui.posJob, tl)
		}
		if f := r.eng.Snapshot().TrigPosFrac; math.Abs(f-0.55) > 1e-9 {
			t.Fatalf("%s: back to the trigger moved it to %g", p.name, f)
		}
		press(t, c, "horizpospush") // left on the second job: the next key resets it
	}
}

// The second job ends when the workspace or its view changes or a sheet
// opens: Position is back on the trigger.
// TRLC-LINKS: REQ-SDS-213
func TestPositionJobResets(t *testing.T) {
	r := newViewRig(t)
	c := r.c
	r.show(toneFrame(""))
	on := func(what string) {
		t.Helper()
		press(t, c, "horizpospush")
		if !c.ui.posJob {
			t.Fatalf("%s: the push did not switch Position", what)
		}
	}
	press(t, c, "f4")
	on("MEASURE")
	press(t, c, "f3")
	if c.ui.posJob {
		t.Fatal("a workspace switch kept the second job")
	}
	on("SPECTRUM")
	press(t, c, "f3") // the next view
	if c.ui.posJob {
		t.Fatal("a view change kept the second job")
	}
	on("SPECTRUM spectrogram")
	press(t, c, "menu")
	press(t, c, "menu")
	if c.ui.posJob || c.ui.ws != wsview.Spectrum {
		t.Fatalf("a sheet kept the second job (ws %d)", c.ui.ws)
	}
	on("SPECTRUM again")
	press(t, c, "f4", "f3")
	if c.ui.posJob {
		t.Fatal("leaving and coming back kept the second job")
	}
}

// The byte picker: the Adjust push triggers on the highlighted byte (the
// match set, the hardware serial trigger armed, a notice); F5 and Menu
// On/Off leave it with a notice and the trigger as it was; the tool line
// says so.
// TRLC-LINKS: REQ-SDS-214
func TestPickerAdjustPushTriggers(t *testing.T) {
	r := newViewRig(t)
	c := r.c
	uart := func() { r.show(uartFrame([]string{"Hi!\r\n", "OK\r\n"}, 460800, 100e-6, 100e-9, 20000)) }
	uart()
	press(t, c, "f2")
	uart()
	turn(c, "adjust", 1) // the second packet: "OK\r\n"
	press(t, c, "adjustpush")
	if st := c.PanelStatus(); st.Mode != "picker" {
		t.Fatalf("Adjust push: mode %s", st.Mode)
	}
	if tl := toolText(c); tl != "Adjust:byte push:trigger on it Position:byte push:back to start Menu:back" {
		t.Fatalf("picker tool line %q", tl)
	}
	press(t, c, "adjustpush")
	if p := r.eng.SerialConfig(); !reflect.DeepEqual(p.Bytes, []int{0x4F}) || r.eng.stats.SerialMode == 0 {
		t.Fatalf("Adjust push picked %+v mode %d", p, r.eng.stats.SerialMode)
	}
	if st := c.PanelStatus(); st.Mode != "workspace" || c.View().Notice != "TRIG ON 4F" {
		t.Fatalf("after the push: %s notice %q", st.Mode, c.View().Notice)
	}
	for _, key := range []string{"f5", "menu"} {
		press(t, c, "adjustpush")
		turn(c, "adjust", 1)
		press(t, c, key)
		st := c.PanelStatus()
		if st.Mode != "workspace" || !strings.HasPrefix(c.View().Notice, "Pick cancelled") {
			t.Fatalf("%s: mode %s notice %q", key, st.Mode, c.View().Notice)
		}
		if p := r.eng.SerialConfig(); !reflect.DeepEqual(p.Bytes, []int{0x4F}) {
			t.Fatalf("%s changed the match: %+v", key, p)
		}
		if st.FKeys[0].Label != wsview.Tags[0] {
			t.Fatalf("%s: the F-keys are not the workspaces': %+v", key, st.FKeys)
		}
	}
	// the sheet's From screen: the Adjust push triggers too; Menu backs out
	press(t, c, "utility")
	goRow(t, c, "bus.match")
	pickKey(t, c, "From screen")
	press(t, c, "adjustpush")
	if p := r.eng.SerialConfig(); !reflect.DeepEqual(p.Bytes, []int{0x48}) || c.View().Notice != "TRIG ON 48" {
		t.Fatalf("sheet pick %+v notice %q", p, c.View().Notice)
	}
	pickKey(t, c, "From screen")
	press(t, c, "menu")
	if st := c.PanelStatus(); st.Mode != "workspace" || !strings.HasPrefix(c.View().Notice, "Pick cancelled") {
		t.Fatalf("sheet picker Menu: %s %q", st.Mode, c.View().Notice)
	}
}

// BUS's Visual view draws the bus and has no packet list: Adjust neither
// moves a hidden selection nor opens the byte picker, Position has no
// "next match", and back on Packets both work again.
// TRLC-LINKS: REQ-SDS-213, REQ-SDS-281
func TestVisualHasNoPacketKnobs(t *testing.T) {
	r := newViewRig(t)
	c := r.c
	uart := func() { r.show(uartFrame([]string{"Hi!\r\n", "OK\r\n"}, 460800, 100e-6, 100e-9, 20000)) }
	uart()
	press(t, c, "f2")
	uart()
	press(t, c, "f2", "f2", "f2")
	if st := mustState(t, c, "BUS", "workspace"); st.View != "Visual" {
		t.Fatalf("view %s, want Visual", st.View)
	}
	// what the tool line says the knobs do here: Position's jobs, no sheet row
	if st := c.PanelStatus(); st.Again != "F2 again: Packets" || strings.Contains(fmt.Sprint(st.Tool), "sheet") {
		t.Fatalf("Visual's tool line %v, again %q", st.Tool, st.Again)
	}
	c.amu.Lock()
	sel := c.ana.list.Selected()
	c.amu.Unlock()
	turn(c, "adjust", 1)
	c.amu.Lock()
	moved := c.ana.list.Selected() != sel
	c.amu.Unlock()
	if moved || !strings.Contains(c.View().Notice, "no packet to pick") {
		t.Fatalf("Adjust in Visual: selection moved %v, notice %q", moved, c.View().Notice)
	}
	press(t, c, "adjustpush")
	if st := c.PanelStatus(); st.Mode != "workspace" || st.View != "Visual" {
		t.Fatalf("Adjust push in Visual: mode %s view %s", st.Mode, st.View)
	}
	press(t, c, "horizpospush")
	if tl := toolText(c); strings.Contains(tl, "next match") || c.ui.posJob {
		t.Fatalf("Position in Visual: %q (second job %v)", tl, c.ui.posJob)
	}
	press(t, c, "f2")
	uart()
	press(t, c, "adjustpush")
	if st := c.PanelStatus(); st.View != "Packets" || st.Mode != "picker" {
		t.Fatalf("back on Packets: view %s mode %s", st.View, st.Mode)
	}
}

// A stream asked for from the web page or the phone while the LCD shows
// Visual puts BUS on Stream alone, and its end on Packets: BUS shows one
// view.
// TRLC-LINKS: REQ-SDS-228, REQ-SDS-281
func TestStreamFromVisualShowsOneView(t *testing.T) {
	r := newViewRig(t)
	c := r.c
	r.show(uartFrame([]string{"Hi!\r\n"}, 460800, 100e-6, 100e-9, 20000))
	press(t, c, "f2", "f2", "f2", "f2")
	if st := mustState(t, c, "BUS", "workspace"); st.View != "Visual" {
		t.Fatalf("view %s, want Visual", st.View)
	}
	c.showBusView(true)
	if st := mustState(t, c, "BUS", "workspace"); st.View != "Stream" || c.ui.busVisual {
		t.Fatalf("stream asked for: view %s, Visual still set %v", st.View, c.ui.busVisual)
	}
	c.showBusView(false)
	if st := mustState(t, c, "BUS", "workspace"); st.View != "Packets" {
		t.Fatalf("stream ended: view %s", st.View)
	}
}
