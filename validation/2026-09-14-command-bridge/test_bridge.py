#!/usr/bin/env python3
"""Verify coherent GPMC-domain command delivery to the acquisition clock."""
from pathlib import Path
import hashlib
import json
import subprocess
import tempfile

root = Path(__file__).resolve().parents[2]
names = ("command_bridge.v", "sim/tb_command_bridge.v")
with tempfile.TemporaryDirectory(prefix="acq-command-bridge-") as directory:
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
    for phase, short_reset in ((phase, short) for short in (0, 1) for phase in range(4)):
        binary = directory / f"test{phase}-{short_reset}"
        subprocess.run(["iverilog", "-g2012", "-s", "tb_command_bridge",
                        f"-Ptb_command_bridge.PHASE={phase}",
                        f"-Ptb_command_bridge.SHORT_RESET={short_reset}", "-o", str(binary),
                        *sources], check=True)
        subprocess.run(["vvp", str(binary)], check=True, timeout=30)
