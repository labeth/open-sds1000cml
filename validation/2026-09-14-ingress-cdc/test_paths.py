#!/usr/bin/env python3
from pathlib import Path
import subprocess
import tempfile
root=Path(__file__).resolve().parents[2]
rtl=root/'fpga/acq_sram'
with tempfile.TemporaryDirectory(prefix='acq-ingress-path-') as directory:
    for phase in (0,2):
        binary=Path(directory)/f'path-{phase}'
        sources=[rtl/name for name in ('word_bridge.v','ingress_fifo.v','ingress_stream.v','ingress_path.v','sim/tb_ingress_path.v')]
        subprocess.run(['iverilog','-g2012','-s','tb',f'-Ptb.PHASE={phase}','-o',str(binary)]+list(map(str,sources)),check=True)
        result=subprocess.run(['vvp',str(binary)],text=True,capture_output=True,timeout=180)
        if result.returncode or result.stdout.count('PASS ingress path')!=4:
            raise RuntimeError(result.stdout+result.stderr)
        print(result.stdout,flush=True)
