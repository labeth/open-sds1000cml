// ENGMODEL-OWNER-UNIT: FU-APP-SUPERRES
package superres

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TRLC-LINKS: REQ-SDS-141, REQ-SDS-050, REQ-SDS-078
func TestStackPageTransferRTL(t *testing.T) {
	for _, phase := range []int{0, 1, 2, 3, 5, 7} {
		t.Run(fmt.Sprint(phase), func(t *testing.T) {
			image := filepath.Join(t.TempDir(), "page.vvp")
			root := filepath.Join("..", "..", "..", "fpga", "acq_sram")
			args := []string{"-g2012", "-s", "tb_stack_page_transfer", fmt.Sprintf("-Ptb_stack_page_transfer.PHASE=%d", phase), "-o", image}
			for _, file := range []string{"stack_page_transfer.v", "host_packer.v", "host_fifo.v", "host_sink.v", "sim/tb_stack_page_transfer.v"} {
				args = append(args, filepath.Join(root, file))
			}
			if out, err := exec.Command("iverilog", args...).CombinedOutput(); err != nil {
				t.Fatalf("compile %v\n%s", err, out)
			}
			out, err := exec.Command("vvp", image).CombinedOutput()
			if err != nil || !strings.Contains(string(out), "PASS page transfer") {
				t.Fatalf("simulate %v\n%s", err, out)
			}
			t.Log(strings.TrimSpace(string(out)))
		})
	}
	for _, bits := range []int{19, 64} {
		t.Run(fmt.Sprintf("sink-ordinal-%d", bits), func(t *testing.T) {
			image := filepath.Join(t.TempDir(), "sink.vvp")
			root := filepath.Join("..", "..", "..", "fpga", "acq_sram")
			args := []string{"-g2012", "-s", "tb_host_sink", fmt.Sprintf("-Ptb_host_sink.ORDINAL_BITS=%d", bits), "-o", image, filepath.Join(root, "host_sink.v"), filepath.Join(root, "host_ram.v"), filepath.Join(root, "sim", "tb_host_sink.v")}
			if out, err := exec.Command("iverilog", args...).CombinedOutput(); err != nil {
				t.Fatalf("compile %v\n%s", err, out)
			}
			out, err := exec.Command("vvp", image).CombinedOutput()
			if err != nil || !strings.Contains(string(out), "PASS host sink") {
				t.Fatalf("simulate %v\n%s", err, out)
			}
			t.Log(strings.TrimSpace(string(out)))
		})
	}
}
