#!/usr/bin/env python3
"""Repeat SRAM candidate reads after factory counter reset, DQ always released."""
import json,subprocess,sys
import boundary as b
b.OUT=b.ROOT/'the acq2 analysis branch'/('2026-09-11-repeat-read-'+sys.argv[1]);b.OUT.mkdir(exist_ok=False)
cy,mv=b.Tap('cyc',16667,603),b.Tap('maxv',16666,240);rows=[]
def level(v,p,x):return b.setbit(v,b.CY[p]['output_cell'],x)
def count():
 c=mv.dr((1<<240)-1)
 return sum(((c>>b.MV[p]['out'])&1)<<i for i,p in enumerate(b.ADDR))
try:
 for rep in range(3):
  cy.ir(1023);mv.ir(1023)
  r=subprocess.run([str(b.ROOT/'ota/dist/otactl'),'-tcp','192.168.1.209:5900','exec','/dev/sramburst','--halt'],capture_output=True,text=True,timeout=15)
  (b.OUT/f'arm-halt-{rep}.txt').write_text(r.stdout+r.stderr);assert r.returncode==0
  mv.sample();cc=cy.sample();cv=b.cy_vector(cc[-1]);start=count()
  v=level(level(level(cv,'G1',1),'K1',1),'K2',0)
  cy.dr(v);cy.ir(15);samples=[]
  for n in range(256):
   for x in (0,1):
    vv=level(v,'K2',x);cy.dr(vv);c=cy.dr(vv)
    if x:samples.append(hex(b.dq_word(c)))
  row={'rep':rep,'before':start,'after':count(),'samples':samples,'cv':hex(cv)};rows.append(row)
  print(rep,start,row['after'],'unique',len(set(samples)),flush=True)
finally:
 cy.ir(1023);mv.ir(1023)
 (b.OUT/'rows.json').write_text(json.dumps(rows,indent=2)+'\n')
 print('Both TAPs BYPASS',flush=True)
if len(rows)==3:
 for rep in (1,2):print('matches after first 16',sum(x==y for x,y in zip(rows[0]['samples'][16:],rows[rep]['samples'][16:])), '/240')
