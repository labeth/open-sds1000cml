#!/usr/bin/env python3
"""Verify archived provenance and result consistency, not device qualification."""
from pathlib import Path
import hashlib
import csv
import json
import re
here=Path(__file__).resolve().parent
root=here.parents[1]
for line in (here/'SHA256SUMS').read_text().splitlines():
    digest,name=line.split('  ',1)
    assert hashlib.sha256((root/name).read_bytes()).hexdigest()==digest,name
passed=here/'passed'
for name in ('word_bridge.v','ingress_fifo.v','ingress_stream.v','ingress_path.v','ingress_cdc.sdc'):
    assert (passed/name).read_bytes()==(root/'fpga/acq_sram'/name).read_bytes(),name
result=json.loads((passed/'result.json').read_text())
assert set(result)=={'setup','hold','recovery','removal','minimum_pulse'}
assert all(value is not None and value>=0 for value in result.values()),result
report=(passed/'probe.sta.rpt').read_text()
match=re.search(r'; Worst-case Slack\s*;\s*([-.0-9]+)\s*;\s*([-.0-9]+)\s*;\s*([-.0-9]+)\s*;\s*([-.0-9]+)\s*;\s*([-.0-9]+)\s*;',report)
assert match
assert tuple(map(float,match.groups()))==tuple(result[k] for k in ('setup','hold','recovery','removal','minimum_pulse'))
assert re.search(r'; M9Ks\s*; 18 / 46', (passed/'probe.fit.rpt').read_text(encoding='latin-1'))
constraints=(passed/'probe.sdc').read_text()
assert 'create_clock -name core -period 4.0' in constraints
assert 'create_clock -name ram -period 8.0' in constraints
rows=list(csv.DictReader((passed/'cdc.tsv').read_text().splitlines(),delimiter='\t'))
assert len(rows)==6 and len({(r['bridge'],r['corner']) for r in rows})==6
assert {r['bridge'] for r in rows}=={'incoming','outgoing'}
for row in rows:
    assert int(row['endpoints'])==36 and float(row['minimum_slack_ns'])>=0
    assert float(row['maximum_data_delay_ns'])<=6
assert 'PASS all 36 payload bits' in (passed/'audit.log').read_text()
assert (here/'bridges.log').read_text().count('throughput source_half=')==8
assert (here/'path-tests.log').read_text().count('PASS ingress path')==8
assert 'PASS timeslice: read=40960' in (here/'transport.log').read_text()
print('PASS hashes, source identity, five timing categories, 18 M9Ks, 72 timed CDC bits per corner, and simulation records')
