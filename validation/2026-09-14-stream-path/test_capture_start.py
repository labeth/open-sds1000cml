#!/usr/bin/env python3
"""Check accepted settings and single-operation reservation across launch delay."""
from pathlib import Path
import ast,hashlib,json,subprocess,tempfile
root=Path(__file__).resolve().parents[2]
# Share the RTL inventory without importing/executing the long test runner.
tree=ast.parse((Path(__file__).with_name('test_capture_engine.py')).read_text())
names=next(ast.literal_eval(n.value) for n in tree.body if isinstance(n,ast.Assign) and any(isinstance(t,ast.Name) and t.id=='names' for t in n.targets))
names=tuple(n for n in names if not n.startswith('sim/'))+('sim/tb_capture_start.v',)
with tempfile.TemporaryDirectory(prefix='acq-start-pipeline-') as directory:
 p=Path(directory);files=[];hashes={}
 for name in names:
  data=(root/'fpga/acq_sram'/name).read_bytes();dest=p/Path(name).name;dest.write_bytes(data)
  files.append(str(dest));hashes[name]=hashlib.sha256(data).hexdigest()
 print(json.dumps(hashes,sort_keys=True),flush=True)
 binary=p/'test'
 subprocess.run(['iverilog','-g2012','-s','tb_capture_start','-o',str(binary),*files],check=True)
 subprocess.run(['vvp',str(binary)],check=True,timeout=30)
