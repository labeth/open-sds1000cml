#!/usr/bin/env python3
"""Verify shadow registers and command decoding across both clock domains."""
from pathlib import Path
import hashlib
import json
import subprocess
import tempfile

root = Path(__file__).resolve().parents[2]
names = ("command_bridge.v", "command_port.v", "sim/tb_command_port.v",
         "sim/tb_command_gpmc.v", "../common/gpmc_slave.v")
with tempfile.TemporaryDirectory(prefix="acq-command-port-") as directory:
    directory = Path(directory)
    sources = []
    hashes = {}
    for name in names:
        data = (root / "fpga/acq_sram" / name).read_bytes()
        path = directory / Path(name).name
        path.write_bytes(data)
        sources.append(str(path))
        hashes[name] = hashlib.sha256(data).hexdigest()
    print(json.dumps(hashes, sort_keys=True), flush=True)
    for enabled in (0, 1):
        for phase in range(4):
            binary = directory / f"test{enabled}-{phase}"
            subprocess.run(["iverilog", "-g2012", "-s", "tb_command_port",
                            f"-Ptb_command_port.ENABLE_STREAM={enabled}",
                            f"-Ptb_command_port.PHASE={phase}", "-o", str(binary),
                            *sources], check=True)
            subprocess.run(["vvp", str(binary)], check=True, timeout=30)
    binary = directory / "gpmc"
    subprocess.run(["iverilog", "-g2012", "-s", "tb_command_gpmc", "-o",
                    str(binary), *sources], check=True)
    subprocess.run(["vvp", str(binary)], check=True, timeout=30)
