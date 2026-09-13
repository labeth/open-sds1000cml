#!/usr/bin/env python3
"""Verify recorded running CRCs and the RAM-only module's ABI evidence."""
from pathlib import Path
import struct, subprocess, hashlib
r=Path(__file__).resolve().parent
ram=(r/'crc-ram.bin').read_bytes()
crcs={}
for line in (r/'crc-addresses.txt').read_text().splitlines():
    address,kind,name=line.split()
    if not name.startswith('__kcrctab_'): continue
    offset=int(address,16)-0xc037a000
    if 0<=offset<=len(ram)-4:
        crcs[name.removeprefix('__kcrctab_')]=struct.unpack_from('<I',ram,offset)[0]
assert len(crcs)==4404
matched=0
for line in (r/'vendor.symvers').read_text().splitlines():
    value,name,*_=line.split()
    if name in crcs:
        assert crcs[name]==int(value,16),name
        matched+=1
assert matched==174
probes={}
for line in (r/'abi-crcs.txt').read_text().splitlines():
    value,kind,name=line.split()
    probes[name.removeprefix('__crc_')]=int(value,16)
required=['__init_waitqueue_head','__mutex_init','complete','wait_for_completion_timeout',
          'edma_alloc_channel','edma_free_channel','edma_start','edma_stop','edma_write_slot']
for name in required: assert probes[name]==crcs[name],name
ko=r.parents[1]/'app/internal/bus/kerneldma/acq_dma_test.ko'
assert hashlib.sha256(ko.read_bytes()).hexdigest()=='896b2c298937b9843ade81005b59c10ba3fb0335a3d6f3ccdccf3fbdf303b062'
assert 'R_ARM_REL32' not in subprocess.check_output(['readelf','-r',str(ko)],text=True)
for line in subprocess.check_output(['modprobe','--dump-modversions',str(ko)],text=True).splitlines():
    value,name=line.split(); assert crcs[name]==int(value,16),name
print('PASS: CRC extraction, 174 cross-checks, synchronization/EDMA ABI probes, module hash and relocations')
