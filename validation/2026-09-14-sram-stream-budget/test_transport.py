#!/usr/bin/env python3
"""Run scheduling experiments using real transport RTL; needs iverilog/vvp."""
from pathlib import Path
import subprocess
import tempfile
import sys

root = Path(__file__).resolve().parents[2]
sources = [root / 'fpga/acq_sram' / p for p in
           ('sim/ddr_model.v', 'transport.v', 'ingress_fifo.v', 'ingress_stream.v', 'sim/tb_sram_timeslice.v')]

def run(name, params, expected, directory):
    binary = Path(directory) / (name + '.vvp')
    subprocess.run(['iverilog', '-g2012', '-s', 'tb', '-o', str(binary)] +
                   [f'-Ptb.{k}={v}' for k, v in params.items()] +
                   [str(p) for p in sources], check=True)
    result = subprocess.run(['vvp', str(binary)], text=True, capture_output=True, timeout=600)
    output = result.stdout + result.stderr
    if expected not in output or (expected.startswith('PASS') and result.returncode != 0):
        raise RuntimeError(name + '\n' + output)
    if not expected.startswith('PASS') and result.returncode == 0:
        raise RuntimeError(name + ': negative test did not fail')
    print(name + ': ' + next(line for line in output.splitlines() if expected in line))

with tempfile.TemporaryDirectory(prefix='acq-timeslice-') as directory:
    small = dict(AW=10, BATCH=16, FIFO=32, HOST_DELAY=1000, ROUNDS=144)
    run('multiple-wraps', small, 'PASS timeslice', directory)
    run('insufficient-fifo', dict(small, FIFO=4), 'ingress overflow', directory)
    run('missing-read-reserve', dict(small, READ_RESERVE=0), 'extra counter circuit', directory)
    if '--full-slow' in sys.argv:
        run('physical-depth-25Mword-writes', dict(ROUNDS=8, WRITE_DIV=10), 'PASS timeslice', directory)
    if '--full' in sys.argv:
        run('physical-depth-10ms-stall', dict(ROUNDS=8), 'PASS timeslice', directory)
