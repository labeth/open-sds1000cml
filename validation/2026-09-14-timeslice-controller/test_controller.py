#!/usr/bin/env python3
from pathlib import Path
import subprocess
import tempfile
import sys
import hashlib
import json
root=Path(__file__).resolve().parents[2]
rtl=root/'fpga/acq_sram'
sources=[rtl/name for name in ('sim/ddr_model.v','transport.v','ordinal_counter.v','timeslice_controller.v',
 'host_path.v','host_ownership.v','host_packer.v','host_fifo.v','host_sink.v','host_ram.v','word_bridge.v','ingress_fifo.v','ingress_stream.v','ingress_path.v','sim/tb_timeslice_controller.v')]
with tempfile.TemporaryDirectory(prefix='acq-controller-') as directory:
    # Freeze all input RTL once: long runs must not mix versions after edits.
    all_files=set(sources+[rtl/'sim/tb_ordinal_counter.v'])
    snapshots={}
    hashes={}
    for source in sorted(all_files):
        data=source.read_bytes()
        target=Path(directory)/source.relative_to(rtl)
        target.parent.mkdir(parents=True,exist_ok=True)
        target.write_bytes(data)
        snapshots[source]=target
        hashes[str(source.relative_to(root))]=hashlib.sha256(data).hexdigest()
    print('source-sha256: '+json.dumps(hashes,sort_keys=True),flush=True)
    def run(name,params,files,expected):
        binary=Path(directory)/name
        subprocess.run(['iverilog','-g2012','-s','tb','-o',str(binary)]+
            [f'-Ptb.{k}={v}' for k,v in params.items()]+[str(snapshots[file]) for file in files],check=True)
        result=subprocess.run(['vvp',str(binary)],text=True,capture_output=True,timeout=900)
        if result.returncode or expected not in result.stdout:raise RuntimeError(result.stdout+result.stderr)
        print(name+': '+result.stdout,flush=True)
    if '--owned-host-full' in sys.argv:
        run('owned-host-physical',{'REAL_HOST':1,'AW':19,'BANK_WORDS':2560,'TARGET':65539,'STALL_BLOCK':0,'STALL_CYCLES':2500000,'REQUIRE_STALL':1},sources,'PASS RTL controller')
        sys.exit(0)
    if '--owned-host-stall' in sys.argv:
        run('owned-host-stall',{'REAL_HOST':1,'AW':13,'BANK_WORDS':2560,'TARGET':10003,'STALL_BLOCK':0,'STALL_CYCLES':250000,'REQUIRE_STALL':1},sources,'PASS RTL controller')
        sys.exit(0)
    if '--owned-host-only' in sys.argv:
        run('owned-host-controller',{'REAL_HOST':1,'AW':13,'BANK_WORDS':2560,'TARGET':10003,'STALL_CYCLES':10000},sources,'PASS RTL controller')
        sys.exit(0)
    run('ordinals',{},[rtl/'ordinal_counter.v',rtl/'sim/tb_ordinal_counter.v'],'PASS 64-bit')
    run('reset-writing',{'RESET_PHASE':1},sources,'PASS active reset')
    run('reset-reading',{'RESET_PHASE':2},sources,'PASS active reset')
    run('consecutive-captures',{'AW':8,'BANK_WORDS':2,'TARGET':201,'STALL_CYCLES':0,'EPOCHS':2},sources,'PASS RTL controller')
    run('wrap-tail-stall',{},sources,'PASS RTL controller')
    run('steady-bandwidth',{'STALL_CYCLES':0},sources,'PASS RTL controller')
    run('counter-lanes',{'AW':12,'BANK_WORDS':640,'TARGET':10003,'STALL_CYCLES':0},sources,'PASS RTL controller')
    run('one-word-tail',{'AW':8,'BANK_WORDS':2,'TARGET':201,'STALL_CYCLES':0},sources,'PASS RTL controller')
    run('unread-protection',{'NO_RELEASE':1},sources,'PASS controller unread protection')
    if '--full-steady' in sys.argv:
        run('physical-steady',{'AW':19,'BANK_WORDS':2560,'TARGET':65539,'STALL_CYCLES':0},sources,'PASS RTL controller')
    if '--full' in sys.argv:
        run('physical-geometry',{'AW':19,'BANK_WORDS':2560,'TARGET':65539,'STALL_CYCLES':2500000},sources,'PASS RTL controller')
    if '--real-host' in sys.argv:
        run('real-host-controller',{'REAL_HOST':1,'AW':14,'BANK_WORDS':2560,'TARGET':20001,'STALL_CYCLES':10000},sources,'PASS RTL controller')
