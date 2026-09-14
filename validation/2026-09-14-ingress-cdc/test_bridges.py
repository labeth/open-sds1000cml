#!/usr/bin/env python3
from pathlib import Path
import hashlib,json
import subprocess
import tempfile
root=Path(__file__).resolve().parents[2]
rtl=root/'fpga/acq_sram'
print(json.dumps({str(p.relative_to(root)): hashlib.sha256(p.read_bytes()).hexdigest()
                  for p in (rtl/'word_bridge.v',rtl/'sim/tb_word_bridge.v')},sort_keys=True),flush=True)
with tempfile.TemporaryDirectory(prefix='acq-bridges-') as directory:
    for source,dest in ((4,2),(2,4)):
        for phase in (0,1,2,3):
            binary=Path(directory)/f'bridge-{source}-{dest}-{phase}'
            subprocess.run(['iverilog','-g2012','-s','tb',f'-Ptb.SOURCE_HALF={source}',
                f'-Ptb.DEST_HALF={dest}',f'-Ptb.PHASE={phase}','-o',str(binary),
                str(rtl/'word_bridge.v'),str(rtl/'sim/tb_word_bridge.v')],check=True)
            result=subprocess.run(['vvp',str(binary)],text=True,capture_output=True,timeout=30)
            if result.returncode or 'PASS word bridge' not in result.stdout:
                raise RuntimeError(result.stdout+result.stderr)
            print(result.stdout,end="",flush=True)
