// ENGMODEL-OWNER-UNIT: FU-APP-SRAMCAPTURE
package sramcapture

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestDecodedEventRTL parses bytes produced by the actual queue/CDC/reader RTL,
// not a Go encoding fixture. A host stall forces loss records into the stream.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-058
func TestDecodedEventRTL(t *testing.T) {
	for _, tool := range []string{"iverilog", "vvp"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s unavailable", tool)
		}
	}
	t.Run("small-overflow", func(t *testing.T) { checkDecodedEventRTL(t, 1, 2, 20, 10, false) })
	t.Run("production-depth-wide-fields", func(t *testing.T) { checkDecodedEventRTL(t, 10, 9, 1568, 20000, true) })
}

// TRLC-LINKS: REQ-SDS-013, REQ-SDS-058
func checkDecodedEventRTL(t *testing.T, aw, hostAW, burst, pause int, expectFull bool) {
	t.Helper()
	dir := t.TempDir()
	rtl := filepath.Join("..", "..", "..", "fpga", "acq_sram")
	image := filepath.Join(dir, "events.vvp")
	output := filepath.Join(dir, "events.bin")
	args := []string{"-g2012", "-s", "tb_decoded_event_transport", "-o", image}
	for name, value := range map[string]int{"AW": aw, "HOST_AW": hostAW, "BURST": burst, "HOST_PAUSE": pause} {
		args = append(args, fmt.Sprintf("-Ptb_decoded_event_transport.%s=%d", name, value))
	}
	if expectFull {
		args = append(args, "-Ptb_decoded_event_transport.EXPECT_FULL=1")
	}
	for _, file := range []string{"decoded_event_transport.v", "decoded_event_queue.v", "decoded_event_bridge.v", "decoded_event_reader.v", "sim/tb_decoded_event_transport.v"} {
		args = append(args, filepath.Join(rtl, file))
	}
	if out, err := exec.Command("iverilog", args...).CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	if out, err := exec.Command("vvp", image, "+output="+output).CombinedOutput(); err != nil {
		t.Fatalf("simulate: %v\n%s", err, out)
	}
	b, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) == 0 || len(b)%DecodedEventBytes != 0 {
		t.Fatal("partial or absent event records")
	}
	var accounted, delivered, lost uint32
	for offset := 0; offset < len(b); offset += DecodedEventBytes {
		e, err := ParseDecodedEvent(b[offset : offset+DecodedEventBytes])
		if err != nil {
			t.Fatal(err)
		}
		if e.Epoch != 9 || e.Sequence != uint32(offset/DecodedEventBytes) || e.Sample != 0x100000000+uint64(accounted) {
			t.Fatalf("identity mismatch: %+v", e)
		}
		switch e.Kind {
		case EventData:
			if e.Protocol != 8 || !e.Valid || e.Value != 0x80000000|accounted || e.Count != 0xfedcba98 {
				t.Fatalf("data mismatch: %+v", e)
			}
			accounted++
			delivered++
		case EventLoss:
			if e.Valid || e.Count == 0 {
				t.Fatalf("invalid loss: %+v", e)
			}
			accounted += e.Count
			lost += e.Count
		default:
			t.Fatalf("unexpected kind %d", e.Kind)
		}
	}
	if accounted != uint32(burst) || delivered == 0 || lost == 0 {
		t.Fatalf("not conserved: delivered=%d lost=%d", delivered, lost)
	}
}
