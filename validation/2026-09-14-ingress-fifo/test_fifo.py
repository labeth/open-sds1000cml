#!/usr/bin/env python3
from pathlib import Path
import subprocess
import tempfile

root=Path(__file__).resolve().parents[2]
rtl=root/'fpga/acq_sram'
with tempfile.TemporaryDirectory(prefix='acq-ingress-') as directory:
    for test in ('ingress_fifo','ingress_stream'):
        for banks,rows in ((9,512),(3,10),(1,8)):
            binary=Path(directory)/f'{test}-{banks}-{rows}.vvp'
            sources=[rtl/'ingress_fifo.v']
            if test=='ingress_stream':sources.append(rtl/'ingress_stream.v')
            sources.append(rtl/'sim'/f'tb_{test}.v')
            subprocess.run(['iverilog','-g2012','-s','tb',f'-Ptb.BANKS={banks}',f'-Ptb.ROWS={rows}',
                            '-o',str(binary)]+[str(p) for p in sources],check=True)
            result=subprocess.run(['vvp',str(binary)],text=True,capture_output=True)
            if result.returncode:raise RuntimeError(result.stdout+result.stderr)
            if 'PASS ingress' not in result.stdout:raise RuntimeError(result.stdout)
            print(result.stdout.splitlines()[0])
