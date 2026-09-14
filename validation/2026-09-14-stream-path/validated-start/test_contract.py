#!/usr/bin/env python3
"""Negative unit checks for integrated start contracts; not transport simulation."""
from pathlib import Path
import hashlib, json, subprocess, tempfile
rtl = Path(__file__).resolve().parent / 'balanced-build/sources'
cases = {
    'finite': ('finite_writer.v', '''sram_finite_writer #(.AW(4),.START_VALIDATED(1)) dut(
.clk(clk),.reset(1'b0),.start(1'b1),.capture_allowed(1'b0),
.halt(1'b0),.pre_count(5'd0),.post_count(5'd1),
.source_fault(1'b0),.source_valid(1'b0),.trigger(1'b0),.source_data(32'd0),
.writer_ready(1'b0),.writer_write_ready(1'b0),.position(4'd0));''',
               'validated finite start violated readiness/geometry contract'),
    'backend': ('capture_engine.v', '''sram_capture_engine #(.AW(4),.START_VALIDATED(1)) dut(
.core_clk(clk),.ram_clk(clk),.host_clk(clk),.reset(1'b1),.start(1'b1),.finite_mode(1'b0));''',
                'validated backend start violated readiness contract'),
}
print(json.dumps({name: hashlib.sha256((rtl/name).read_bytes()).hexdigest()
                  for name, _, _ in cases.values()}, sort_keys=True), flush=True)
with tempfile.TemporaryDirectory(prefix='acq-start-contract-') as directory:
    p = Path(directory)
    for name, (source, instance, message) in cases.items():
        (p/'tb.v').write_text('module tb; reg clk=0; always #2 clk=~clk;\n' + instance +
                             '\ninitial begin #20;$fatal(1,"assertion did not fire");end endmodule\n')
        # Ignore unrelated child datapaths: each case forces readiness false
        # at this module boundary. Full integration tests exercise real children.
        subprocess.run(['iverilog', '-g2012', '-i', '-s', 'tb', '-o', str(p/'test'),
                        str(p/'tb.v'), str(rtl/source)], check=True)
        result = subprocess.run(['vvp', str(p/'test')], capture_output=True, text=True)
        if result.returncode == 0 or message not in result.stdout:
            raise AssertionError((name, result.returncode, result.stdout, result.stderr))
        print(f'PASS {name}: invalid qualified start triggers contract assertion', flush=True)
