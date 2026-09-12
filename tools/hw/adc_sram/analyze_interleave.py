#!/usr/bin/env python3
"""Summarize full ADC recalls against atomic per-core snapshots; no device access."""
import hashlib,json,pathlib,statistics,sys
root=pathlib.Path(sys.argv[1])
snapshot=json.loads((root/'snapshot.json').read_text())['cores']
reference=[statistics.mean(row[i] for row in snapshot) for i in range(10)]
order=[6,9,4,1,2,7,8,5,0,3]
results=[]
for path in sorted(root.glob('adc-*.bin')):
 data=path.read_bytes();assert len(data)==2097152
 means=[statistics.mean(data[i::10]) for i in range(10)]
 errors={shift:sum((means[i]-reference[order[(i+shift)%10]])**2 for i in range(10)) for shift in range(0,10,2)}
 shift=min(errors,key=errors.get)
 rotated=order[shift:]+order[:shift]
 rows=[]
 for phase,core in enumerate(rotated):
  values=data[phase::10];mean=statistics.mean(values)
  rows.append({'frame_byte':phase,'pair':core//2+1,'channel':core%2+1,'samples':len(values),'mean':mean,'stdev':statistics.pstdev(values),'minimum':min(values),'maximum':max(values),'snapshot_mean':reference[core],'delta_from_snapshot':mean-reference[core]})
 results.append({'file':path.name,'bytes':len(data),'sha256':hashlib.sha256(data).hexdigest(),'inferred_frame_rotation_bytes':shift,'rotation_squared_errors':errors,'cores':rows})
assert results,'No complete ADC recalls found'
(root/'adc-statistics.json').write_text(json.dumps(results,indent=2)+'\n')
for row in results:
 print(row['file'], 'mean differences from per-core snapshot:',[round(r['delta_from_snapshot'],3) for r in row['cores']])
print('These DC statistics do not qualify analog aperture skew or AC bandwidth.')
