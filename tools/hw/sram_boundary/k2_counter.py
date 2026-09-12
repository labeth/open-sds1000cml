#!/usr/bin/env python3
"""Pulse K2 under Cyclone EXTEST, retaining functional MAX V under SAMPLE."""
import json,time,pathlib,subprocess,sys
import boundary as b
b.OUT=b.ROOT/'the acq2 analysis branch'/('2026-09-11-k2-counter-'+sys.argv[1]);b.OUT.mkdir(exist_ok=False)
cy,mv=b.Tap('cyc',16667,603),b.Tap('maxv',16666,240)
rows=[]
def count(cap):return sum(((cap>>b.MV[p]['out'])&1)<<i for i,p in enumerate(b.ADDR))
def capture(label):
 caps=[mv.dr((1<<240)-1) for _ in range(3)]
 row={'label':label,'time_ns':time.time_ns(),'caps':list(map(hex,caps)),'counter':list(map(count,caps))}
 rows.append(row);print(label,row['counter'],flush=True)
try:
 if len(sys.argv)>2:
  r=subprocess.run([str(b.ROOT/'ota/dist/otactl'),'-tcp','192.168.1.209:5900','exec','/dev/sramburst','--run'],capture_output=True,text=True,timeout=15)
  (b.OUT/'arm-run.txt').write_text(r.stdout+r.stderr)
  if r.returncode:raise RuntimeError('ARM run failed')
 mc=mv.sample();cc=cy.sample();cv=b.cy_vector(cc[-1]);
 assert ((cv>>b.CY['K2']['control_cell'])&1)==0
 (b.OUT/'initial.json').write_text(json.dumps({'mv':list(map(hex,mc)),'cy':list(map(hex,cc)),'cv':hex(cv)},indent=2)+'\n')
 capture('functional-before')
 cy.dr(cv);cy.ir(15)
 for level in (0,1):
  cv=b.setbit(cv,b.CY['K2']['output_cell'],level);cy.dr(cv)
  capture('K2-held-'+str(level));time.sleep(.1);capture('K2-held-delayed-'+str(level))
 for n in ((8192,)*16 if len(sys.argv)>2 else (1,2,4,8,16,32,64,128,256)):
  low=b.setbit(cv,b.CY['K2']['output_cell'],0);high=b.setbit(cv,b.CY['K2']['output_cell'],1)
  cy.sock.settimeout(30)
  cy.cmd(f'for {{set k2pulse 0}} {{$k2pulse < {n}}} {{incr k2pulse}} {{drscan cyc.tap 603 0x{low:x}; drscan cyc.tap 603 0x{high:x}}}')
  capture('pulses-'+str(n))
  r=subprocess.run([str(b.ROOT/'ota/dist/otactl'),'-tcp','192.168.1.209:5900','ping'],capture_output=True,text=True,timeout=10)
  if r.returncode:raise RuntimeError('OTA health check failed: '+r.stderr)
finally:
 cy.ir(1023);mv.ir(1023)
 (b.OUT/'rows.json').write_text(json.dumps(rows,indent=2)+'\n')
 print('Both TAPs BYPASS',flush=True)
