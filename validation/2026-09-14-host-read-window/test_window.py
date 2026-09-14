#!/usr/bin/env python3
"""Exercise owned-bank prefetch using the actual host RAM and GPMC slave."""
from pathlib import Path
import argparse
import hashlib
import json
import subprocess
import tempfile

parser = argparse.ArgumentParser()
parser.add_argument("--vendor-library", type=Path)
args = parser.parse_args()
root = Path(__file__).resolve().parents[2]
names = ("fpga/common/gpmc_slave.v", "fpga/acq_sram/host_ram.v",
         "fpga/acq_sram/host_read_window.v", "fpga/acq_sram/sim/tb_host_read_window.v")
with tempfile.TemporaryDirectory(prefix="acq-host-read-window-") as directory:
    directory = Path(directory)
    sources = []
    hashes = {}
    for name in names:
        data = (root / name).read_bytes()
        path = directory / Path(name).name
        path.write_bytes(data)
        sources.append(str(path))
        hashes[name] = hashlib.sha256(data).hexdigest()
    extra = []
    if args.vendor_library:
        hashes["vendor:altera_mf.v"] = hashlib.sha256(args.vendor_library.read_bytes()).hexdigest()
        extra = ["-DALTERA_RESERVED_QIS", str(args.vendor_library.resolve())]
    print(json.dumps(hashes, sort_keys=True), flush=True)
    binary = directory / "test"
    subprocess.run(["iverilog", "-g2012", "-s", "tb_host_read_window", "-o",
                    str(binary), *extra, *sources], check=True)
    subprocess.run(["vvp", str(binary)], check=True, timeout=60)
