#!/usr/bin/env python3
"""Measure direct/inverted Cyclone control-to-MAX V input links."""
import json,subprocess,sys
import boundary as b
b.OUT=b.ROOT/'the acq2 analysis branch'/('2026-09-11-control-links-'+sys.argv[1]);b.OUT.mkdir(exist_ok=False)
cy,mv=b.Tap('cyc',16667,603),b.Tap('maxv',16666,240)
rows=[]
try:
 cc=cy.sample();mc=mv.sample();cv=b.cy_vector(cc[-1])
 (b.OUT/'initial.json').write_text(json.dumps({'cy':list(map(hex,cc)),'mv':list(map(hex,mc)),'cv':hex(cv)},indent=2)+'\n')
 cy.dr(cv);cy.ir(15)
 controls=['D2','D1','G2','F1','G1','K1','K2','F2','J2','A11']
 assert all(not ((cv>>b.CY[p]['control_cell'])&1) for p in controls)
 for ball in controls:
  groups=[]
  for level in (0,1,0,1):
   cy.dr(b.setbit(cv,b.CY[ball]['output_cell'],level))
   groups.append([mv.dr((1<<240)-1) for _ in range(3)])
  hits=[]
  for pin,t in b.MV.items():
   if any((v>>t['ctl'])&1==0 for g in groups for v in g):continue
   vals=[[(v>>t['in'])&1 for v in g] for g in groups]
   for inversion in (0,1):
    if vals==[[level^inversion]*3 for level in (0,1,0,1)]:hits.append({'pin':pin,'inverted':bool(inversion)})
  row={'ball':ball,'hits':hits,'caps':[[hex(v) for v in g] for g in groups]};rows.append(row);print(ball,hits,flush=True)
  cy.dr(cv)
  r=subprocess.run([str(b.ROOT/'ota/dist/otactl'),'-tcp','192.168.1.209:5900','ping'],capture_output=True,text=True,timeout=10)
  if r.returncode:raise RuntimeError('OTA health check failed')
finally:
 cy.ir(1023);mv.ir(1023)
 (b.OUT/'rows.json').write_text(json.dumps(rows,indent=2)+'\n')
 print('Both TAPs BYPASS',flush=True)
