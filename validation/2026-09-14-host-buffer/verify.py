#!/usr/bin/env python3
from pathlib import Path
import hashlib,json,re
root=Path(__file__).resolve().parents[2]
here=Path(__file__).resolve().parent
for name,want in json.loads((here/'manifest.json').read_text()).items():
 assert hashlib.sha256((root/name).read_bytes()).hexdigest()==want,name
passed=here/'ram-passed'
result=json.loads((passed/'result.json').read_text())
assert result['m9k_blocks']==20
assert result['source_sha256']==hashlib.sha256((root/'fpga/acq_sram/host_ram.v').read_bytes()).hexdigest()
assert (passed/'host_ram.v').read_bytes()==(root/'fpga/acq_sram/host_ram.v').read_bytes()
sta=(passed/'probe.sta.rpt').read_text(encoding='latin-1')
m=re.search(r'; Worst-case Slack\s*;\s*([-.0-9]+)\s*;\s*([-.0-9]+)\s*;([^;\n]*);([^;\n]*);\s*([-.0-9]+)\s*;',sta)
assert m
values=[None if x.strip()=='N/A' else float(x) for x in m.groups()]
assert values==[result['timing'][k] for k in ('setup','hold','recovery','removal','minimum_pulse')]
assert all(x is None or x>=0 for x in values)
fit=(passed/'probe.fit.rpt').read_text(encoding='latin-1')
assert re.search(r'; M9Ks\s*;\s*20\s*/\s*46',fit)
rows=[x for x in fit.splitlines() if 'Simple Dual Port' in x and re.search(r'; sram_host_ram:dut\|altsyncram:sections\[',x)]
assert len(rows)==5
assert {int(re.search(r'sections\[(\d)\]',x).group(1)) for x in rows}==set(range(5))
for row in rows:
 assert re.search(r';\s*Simple Dual Port\s*;\s*Dual Clocks\s*;\s*512\s*;\s*64\s*;\s*512\s*;\s*64\s*;',row)
 assert re.search(r';\s*32768\s*;\s*4\s*;\s*None\s*;',row)
sdc=(passed/'probe.sdc').read_text()
assert '-period 8.0' in sdc and '-period 10.0' in sdc
assert 'set_false_path' not in sdc and 'set_clock_groups' not in sdc
log=(here/'ram-tests.log').read_text()
assert 'PASS host RAM: 30757 halfword responses' in log
hashes=json.loads(log.splitlines()[0].removeprefix('source-sha256: '))
for name,want in hashes.items():assert hashlib.sha256((root/name).read_bytes()).hexdigest()==want
print('PASS host RAM evidence: 20 M9Ks, 125/100 MHz timing, 30757 responses, source hashes')
