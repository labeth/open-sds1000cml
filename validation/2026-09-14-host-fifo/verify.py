#!/usr/bin/env python3
from pathlib import Path
import hashlib,json,re,csv
root=Path(__file__).resolve().parents[2];here=Path(__file__).resolve().parent;passed=here/'passed'
for name,want in json.loads((here/'manifest.json').read_text()).items():
 assert hashlib.sha256((root/name).read_bytes()).hexdigest()==want,name
result=json.loads((passed/'result.json').read_text())
assert result['cdc_audit']=='passed' and result['m9k_blocks']==0
for name,want in result['sources'].items():
 assert hashlib.sha256((root/'fpga/acq_sram'/name).read_bytes()).hexdigest()==want
 assert (root/'fpga/acq_sram'/name).read_bytes()==(passed/name).read_bytes()
sta=(passed/'probe.sta.rpt').read_text(encoding='latin-1')
m=re.search(r'; Worst-case Slack\s*;\s*([-.0-9]+)\s*;\s*([-.0-9]+)\s*;([^;\n]*);([^;\n]*);\s*([-.0-9]+)\s*;',sta);assert m
values=[None if x.strip()=='N/A' else float(x) for x in m.groups()]
assert values==[result['timing'][k] for k in ('setup','hold','recovery','removal','minimum_pulse')]
assert all(x is not None and x>=0 for x in values)
fit=(passed/'probe.fit.rpt').read_text(encoding='latin-1')
assert re.search(r'; M9K(?:s| blocks)\s*;\s*0\s*/',fit)
sdc=(passed/'probe.sdc').read_text()
assert '-period 4.0' in sdc and '-period 8.0' in sdc and 'set_clock_groups' not in sdc
assert sdc.count('set_false_path')==1 and 'set_false_path -from [get_ports reset]' in sdc
expected={'slots':(640,6),'output':(80,6),'wgray_first':(4,4),'rgray_first':(4,4),'wgray_second':(4,8),'rgray_second':(4,4)}
rows=list(csv.DictReader((passed/'cdc.tsv').open(),delimiter='\t'))
corners={r['corner'] for r in rows};assert len(corners)==3 and len(rows)==18
assert {(r['group'],r['corner']) for r in rows}=={(g,c) for g in expected for c in corners}
for row in rows:
 count,limit=expected[row['group']]
 assert int(row['endpoints'])==count and float(row['minimum_slack_ns'])>=0
 assert float(row['maximum_data_delay_ns'])<=limit
assert 'PASS host FIFO: 640 stored bits' in (passed/'audit.log').read_text()
log=(here/'tests.log').read_text()
for phase in range(4):
 for stall in range(2):assert f'PASS host FIFO phase={phase} sync_stall={stall}:' in log
bursts=re.findall(r'packed burst phase=\d words=(\d+) max_pending=(\d+)',log)
assert len(bursts)==8 and all(int(n)==2562 and int(depth)<=8 for n,depth in bursts)
for name,want in json.loads(log.splitlines()[0].removeprefix('source-sha256: ')).items():
 assert hashlib.sha256((root/name).read_bytes()).hexdigest()==want
print('PASS host FIFO evidence: eight simulations, zero M9Ks, 250/125 MHz timing, full CDC audit')
