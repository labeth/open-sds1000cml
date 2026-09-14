#!/usr/bin/env python3
"""Compare pipelined triggers with an independent sequential sample oracle."""
from pathlib import Path
import hashlib, json, subprocess, tempfile
root = Path(__file__).resolve().parents[2] / 'fpga/acq_sram'
with tempfile.TemporaryDirectory(prefix='acq-trigger-') as directory:
    dest = Path(directory)
    files = []
    hashes = {}
    for name in ('acquisition_path.v', 'sim/tb_trigger_pipeline.v'):
        data = (root / name).read_bytes()
        path = dest / Path(name).name
        path.write_bytes(data)
        files.append(str(path))
        hashes[name] = hashlib.sha256(data).hexdigest()
    print(json.dumps(hashes, sort_keys=True), flush=True)
    subprocess.run(['iverilog', '-g2012', '-s', 'tb_trigger_pipeline', '-o', str(dest / 'test'), *files], check=True)
    subprocess.run(['vvp', str(dest / 'test')], check=True, timeout=60)
