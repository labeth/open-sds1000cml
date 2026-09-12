#!/usr/bin/env python3
"""Record core timing and exact pin placement; does not claim external timing closure."""
import pathlib,re,json,hashlib,sys
out=pathlib.Path(sys.argv[1]).resolve()
sta=(out/'output_files/bench.sta.rpt').read_text();fit=(out/'output_files/bench.fit.rpt').read_text(encoding='latin1')
rows=[]
for block in re.finditer(r';\s*((?:Slow|Fast)[^;\n]* Model (?:Setup|Hold|Recovery|Removal|Minimum Pulse Width) Summary)\s*;\n(.*?)(?:\n\n)',sta,re.S):
 for clock,slack in re.findall(r';\s*([^;\n]+?)\s*;\s*(-?\d+\.\d+)\s*;',block[2]):
  rows.append({'corner_check':block[1].strip(),'clock':clock.strip(),'slack_ns':float(slack)})
assert len(rows)>=6,'No complete timing summaries'
assert any('Minimum Pulse Width' in r['corner_check'] for r in rows),'Missing pulse-width checks'
qsf=(out/'bench.qsf').read_text();ports=re.findall(r'set_location_assignment PIN_(\w+) -to (\S+)',qsf)
actual={}
for line in fit.splitlines():
 c=[v.strip() for v in line.split(';')]
 if len(c)>6 and c[5] in ('input','output','bidir'):actual[c[1]]=c[4]
assert all(actual.get(ball)==port for ball,port in ports),'Assigned pin differs from fitter result'
strength={}
for setting,port in re.findall(r'set_instance_assignment -name CURRENT_STRENGTH_NEW (.*?) -to (\S+)',qsf):
 if port.startswith('dq['):
  value='4mA' if 'MINIMUM' in setting else setting.strip('"').replace('MA','mA')
  if port=='dq[*]':strength.update({f'dq[{i}]':value for i in range(32)})
  else:strength[port]=value
for port,value in strength.items():
 lines=[line for line in fit.splitlines() if line.split(';')[1:2] and line.split(';')[1].strip()==port]
 assert any(value in [c.strip() for c in line.split(';')] for line in lines),f'{port} current strength differs from requested {value}'
d={'rbf_sha256':hashlib.sha256((out/'bench.rbf').read_bytes()).hexdigest(),'source_sha256':hashlib.sha256((out/'bench.v').read_bytes()).hexdigest(),'assigned_pins':len(ports),'dq_drive':strength,'timing':rows,'core_timing_pass':all(x['slack_ns']>=0 for x in rows),'external_timing':'Not statically constrained; requires measured board validation.'}
(out/'audit.json').write_text(json.dumps(d,indent=2)+'\n')
print('Core timing:',d['core_timing_pass'],'worst slack:',min(x['slack_ns'] for x in rows),'pins verified:',len(ports))
if not d['core_timing_pass']:sys.exit(2)
