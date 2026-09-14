#!/usr/bin/env python3
"""Check archived evidence consistency; does not replace simulation or fitting."""
from pathlib import Path
import hashlib
import json
import re

here=Path(__file__).resolve().parent
root=here.parents[1]
for line in (here/'SHA256SUMS').read_text().splitlines():
    digest,name=line.split('  ',1)
    assert hashlib.sha256((root/name).read_bytes()).hexdigest()==digest,name
pattern=r'; Worst-case Slack\s*;\s*([-.0-9]+)\s*;\s*([-.0-9]+)\s*;[^;\n]*;[^;\n]*;\s*([-.0-9]+)\s*;'
def slack(path):
    match=re.search(pattern,path.read_text())
    assert match,path
    return tuple(map(float,match.groups()))
passed=here/'qualified-isolated-125mhz'
for name in ('ingress_fifo.v','ingress_stream.v'):
    assert (passed/name).read_bytes()==(root/'fpga/acq_sram'/name).read_bytes(),name
values=slack(passed/'probe.sta.rpt')
assert min(values)>=0,values
result=json.loads((passed/'result.json').read_text())
assert result['clock_mhz']==125
assert values==tuple(result[k] for k in ('setup_slack_ns','hold_slack_ns','min_pulse_slack_ns'))
assert re.search(r'; M9Ks\s*; 18 / 46', (passed/'probe.fit.rpt').read_text(encoding='latin-1'))
assert slack(here/'rejected-250mhz/probe.sta.rpt')[2]<0
assert '4.201' in (here/'pulse-width.rpt').read_text()
assert (here/'tests.log').read_text().count('PASS ingress')==6
for name in ('transport-final.log','transport-25mword.log'):
    assert 'PASS timeslice:' in (here/name).read_text(),name
print('PASS archived hashes, positive 125 MHz timing, rejected 250 MHz RAM period, and simulation records')
