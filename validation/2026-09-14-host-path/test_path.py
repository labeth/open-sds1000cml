#!/usr/bin/env python3
"""Snapshot and run the real packer/FIFO/sink/RAM functional path."""
import hashlib
import json
from pathlib import Path
import subprocess
import tempfile
ROOT = Path(__file__).resolve().parents[2]
FILES = ['fpga/acq_sram/' + name for name in (
    'host_path.v', 'host_packer.v', 'host_fifo.v', 'host_sink.v', 'host_ram.v', 'host_ownership.v',
    'sim/tb_host_path.v', 'sim/tb_host_faults.v')]
with tempfile.TemporaryDirectory(prefix='acq-host-path-') as directory:
    tmp = Path(directory)
    sources = []
    hashes = {}
    for name in FILES:
        content = (ROOT / name).read_bytes()
        dest = tmp / Path(name).name
        dest.write_bytes(content)
        sources.append(str(dest))
        hashes[name] = hashlib.sha256(content).hexdigest()
    print(json.dumps({'source_sha256': hashes}, indent=2), flush=True)
    for phase in range(4):
        binary = tmp / f'phase{phase}'
        subprocess.run(['iverilog', '-g2012', '-s', 'tb_host_path',
                        f'-Ptb_host_path.PHASE={phase}', '-o', str(binary),
                        *sources], check=True)
        subprocess.run(['vvp', str(binary)], check=True)
    for phase in range(4):
        binary = tmp / f'faults{phase}'
        subprocess.run(['iverilog', '-g2012', '-s', 'tb_host_faults',
                        f'-Ptb_host_faults.PHASE={phase}', '-o', str(binary),
                        *sources], check=True)
        subprocess.run(['vvp', str(binary)], check=True)
