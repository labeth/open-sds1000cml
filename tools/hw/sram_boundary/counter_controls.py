#!/usr/bin/env python3
"""Test measured G2/G1/K2 links with active MAX V logic and released DQ."""
import json,subprocess,sys
import boundary as b
b.OUT=b.ROOT/'the acq2 analysis branch'/('2026-09-11-counter-controls-'+sys.argv[1]);b.OUT.mkdir(exist_ok=False)
cy,mv=b.Tap('cyc',16667,603),b.Tap('maxv',16666,240);rows=[]
def read(label):
 caps=[mv.dr((1<<240)-1) for _ in range(3)]
 counts=[sum(((c>>b.MV[p]['out'])&1)<<i for i,p in enumerate(b.ADDR)) for c in caps]
 rows.append({'label':label,'caps':list(map(hex,caps)),'counts':counts});print(label,counts,flush=True)
def level(v,p,x):return b.setbit(v,b.CY[p]['output_cell'],x)
try:
 r=subprocess.run([str(b.ROOT/'ota/dist/otactl'),'-tcp','192.168.1.209:5900','exec','/dev/sramburst','--run'],capture_output=True,text=True,timeout=15)
 (b.OUT/'arm-run.txt').write_text(r.stdout+r.stderr);assert r.returncode==0
 mv.sample();cc=cy.sample();cv=b.cy_vector(cc[-1]);read('functional')
 (b.OUT/'initial.json').write_text(json.dumps({'cy':list(map(hex,cc)),'cv':hex(cv)},indent=2)+'\n')
 cy.dr(cv);cy.ir(15);read('extest-entry')
 for gate in (0,1,0):
  v=level(cv,'G1',gate);cy.dr(v);read(f'G1={gate}')
  for clock in ('G2','K2','both'):
   for n in (1,4,16,64):
    for _ in range(n):
     for x in (0,1):
      vv=v
      for pin in (('G2','K2') if clock=='both' else (clock,)):vv=level(vv,pin,x)
      cy.dr(vv)
    read(f'G1={gate} {clock} pulses={n}')
    r=subprocess.run([str(b.ROOT/'ota/dist/otactl'),'-tcp','192.168.1.209:5900','ping'],capture_output=True,text=True,timeout=10)
    if r.returncode:raise RuntimeError('OTA check failed')
finally:
 cy.ir(1023);mv.ir(1023)
 (b.OUT/'rows.json').write_text(json.dumps(rows,indent=2)+'\n')
 print('Both TAPs BYPASS',flush=True)
