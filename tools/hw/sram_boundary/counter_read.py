#!/usr/bin/env python3
"""Read released Cyclone DQ while stepping functional MAX V counter."""
import json,subprocess,sys
import boundary as b
b.OUT=b.ROOT/'the acq2 analysis branch'/('2026-09-11-counter-read-'+sys.argv[1]);b.OUT.mkdir(exist_ok=False)
cy,mv=b.Tap('cyc',16667,603),b.Tap('maxv',16666,240);rows=[]
def level(v,p,x):return b.setbit(v,b.CY[p]['output_cell'],x)
def count():
 c=mv.dr((1<<240)-1)
 return sum(((c>>b.MV[p]['out'])&1)<<i for i,p in enumerate(b.ADDR))
try:
 r=subprocess.run([str(b.ROOT/'ota/dist/otactl'),'-tcp','192.168.1.209:5900','exec','/dev/sramburst','--run'],capture_output=True,text=True,timeout=15)
 (b.OUT/'arm-run.txt').write_text(r.stdout+r.stderr);assert r.returncode==0
 mv.sample();cc=cy.sample();cv=b.cy_vector(cc[-1])
 (b.OUT/'initial.json').write_text(json.dumps({'cy':list(map(hex,cc)),'cv':hex(cv)},indent=2)+'\n')
 assert all((cv>>b.CY[p]['control_cell'])&1 for p in b.DQ+b.GP)
 cy.dr(cv);cy.ir(15)
 for k1 in (0,1):
  for g1 in (0,1):
   v=level(level(cv,'K1',k1),'G1',g1);cy.dr(v);before=count();samples=[]
   for n in range(128):
    for x in (0,1):
     vv=level(v,'K2',x);cy.dr(vv);c=cy.dr(vv)
     samples.append({'edge':x,'dq':hex(b.dq_word(c))})
   row={'K1':k1,'G1':g1,'before':before,'after':count(),'samples':samples};rows.append(row)
   print(k1,g1,before,row['after'],'DQ',sorted({r['dq'] for r in samples}),flush=True)
   r=subprocess.run([str(b.ROOT/'ota/dist/otactl'),'-tcp','192.168.1.209:5900','ping'],capture_output=True,text=True,timeout=10)
   if r.returncode:raise RuntimeError('OTA failed')
finally:
 cy.ir(1023);mv.ir(1023)
 (b.OUT/'rows.json').write_text(json.dumps(rows,indent=2)+'\n')
 print('Both TAPs BYPASS',flush=True)
