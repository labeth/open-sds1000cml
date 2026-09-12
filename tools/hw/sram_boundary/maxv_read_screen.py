#!/usr/bin/env python3
"""Volatile MAX V control screen, with Cyclone DQ always released."""
import json
from boundary import *
s=json.loads((OUT/'state.json').read_text())
cv,vv=int(s['cy_vector'],16),int(s['mv_vector'],16)
cy,mv=Tap('cyc',16667,603),Tap('maxv',16666,240)
controls=[21,12,67,42,41,35,34]
clocks=[100,97,88,37,33,8,7,6,5,4,3]
assert all((cv>>CY[b]['control_cell'])&1 for b in DQ+GP)
assert all(not ((vv>>MV[p]['ctl'])&1) for p in controls+clocks+ADDR)
base=vv
for p in ADDR:base=setbit(base,MV[p]['out'],0)
rows=[]
try:
 for mask in range(128):
  v=base
  for i,p in enumerate(controls):v=setbit(v,MV[p]['out'],(mask>>i)&1)
  for pin in clocks:
   mv.dr(v)
   for _ in range(4):
    for level in (0,1):mv.dr(setbit(v,MV[pin]['out'],level))
   words=[dq_word(cy.dr(cv)) for _ in range(3)]
   rows.append({'mask':mask,'clock':pin,'dq':list(map(hex,words))})
   if any(w!=0xffffffff for w in words):
    print('RESPONDER',rows[-1],flush=True)
    raise SystemExit(0)
  if mask%16==15:print('Completed',len(rows),'conditions',flush=True)
finally:
 mv.dr(vv);cy.dr(cv)
 (OUT/'maxv-read-screen.json').write_text(json.dumps({'controls':controls,'rows':rows},indent=2)+'\n')
 print('EXTEST baseline restored',flush=True)
