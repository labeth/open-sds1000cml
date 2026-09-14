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
parser.add_argument("--port", action="store_true", help="test the GPMC register mapping")
args = parser.parse_args()
root = Path(__file__).resolve().parents[2]
names = ("fpga/common/gpmc_slave.v", "fpga/acq_sram/host_ram.v",
         "fpga/acq_sram/host_read_window.v", "fpga/acq_sram/sim/tb_host_read_window.v")
if args.port:
    names = names[:-1] + ("fpga/acq_sram/host_read_port.v", "fpga/acq_sram/sim/tb_host_read_port.v")
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
    top = "tb_host_read_port" if args.port else "tb_host_read_window"
    subprocess.run(["iverilog", "-g2012", "-s", top, "-o",
                    str(binary), *extra, *sources], check=True)
    subprocess.run(["vvp", str(binary)], check=True, timeout=60)
