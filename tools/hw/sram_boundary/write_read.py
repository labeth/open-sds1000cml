#!/usr/bin/env python3
"""Volatile SRAM pattern write/read using factory MAX V counter and EXTEST."""
import json,subprocess,sys
import boundary as b
b.OUT=b.ROOT/'the acq2 analysis branch'/('2026-09-11-write-read-'+sys.argv[1]);b.OUT.mkdir(exist_ok=False)
cy,mv=b.Tap('cyc',16667,603),b.Tap('maxv',16666,240);result={}
def level(v,p,x):return b.setbit(v,b.CY[p]['output_cell'],x)
def count():
 c=mv.dr((1<<240)-1)
 return sum(((c>>b.MV[p]['out'])&1)<<i for i,p in enumerate(b.ADDR))
def reset(label):
 cy.ir(1023);mv.ir(1023)
 r=subprocess.run([str(b.ROOT/'ota/dist/otactl'),'-tcp','192.168.1.209:5900','exec','/dev/sramburst','--halt'],capture_output=True,text=True,timeout=15)
 (b.OUT/f'arm-halt-{label}.txt').write_text(r.stdout+r.stderr);assert r.returncode==0
 mv.sample();cc=cy.sample();cv=b.cy_vector(cc[-1]);assert count()==0
 assert all(not ((cc[-1]>>b.CY[p]['control_cell'])&1) for p in b.DQ)
 return cv
def clock(v):
 cy.dr(level(v,'K2',0));cy.dr(level(v,'K2',1))
 return cy.dr(level(v,'K2',1))
try:
 seed=int(sys.argv[2],0) if len(sys.argv)>2 else 0x6b52a9d3;words=[]
 for n in range(256):
  seed^=(seed<<13)&0xffffffff;seed^=seed>>17;seed^=(seed<<5)&0xffffffff;seed&=0xffffffff
  words.append(seed)
 result['expected']=list(map(hex,words))
 cv=reset('write');v=level(level(level(cv,'K1',0),'G1',0),'K2',0)
 cy.dr(v);cy.ir(15)
 # Stop external read driving before enabling the Cyclone DQ drivers.
 flush=[hex(b.dq_word(clock(v))) for _ in range(8)];result['write_turnaround']=flush
 assert all(w=='0xffffffff' for w in flush[-4:]),'Read driver has not released the bus'
 for p in b.DQ:v=b.setbit(v,b.CY[p]['control_cell'],0)
 v=level(v,'G1',1);cy.dr(level(v,'K2',1))
 for word in words:
  for i,p in enumerate(b.DQ):v=b.setbit(v,b.CY[p]['output_cell'],(word>>i)&1)
  cy.dr(level(v,'K2',1)) # settle data before either clock edge
  clock(v)
 result['after_write_counter']=count();print('write count',result['after_write_counter'],flush=True)
 # Release every DQ output while still in write mode before resetting/read mode.
 for p in b.DQ:v=b.setbit(v,b.CY[p]['control_cell'],1)
 cy.dr(v)
 cv=reset('read');v=level(level(level(cv,'K1',1),'G1',1),'K2',0)
 cy.dr(v);cy.ir(15)
 reads=[b.dq_word(clock(v)) for _ in range(280)]
 result['read']=list(map(hex,reads));result['after_read_counter']=count()
 matches=[]
 for offset in range(-8,9):
  pairs=[(i,i+offset) for i in range(16,240) if 0<=i+offset<len(reads)]
  matches.append({'read_index_minus_write_index':offset,'matches':sum(words[i]==reads[j] for i,j in pairs),'tested':len(pairs)})
 result['alignment']=matches
 print('best alignment',max(matches,key=lambda x:x['matches']),flush=True)
 print('first read',list(map(hex,reads[:12])),flush=True)
 result['mismatch_indices_at_offset_1']=[i for i,w in enumerate(words) if reads[i+1]!=w]
 assert result['after_write_counter']==256
 assert result['after_read_counter']==280
 assert set(result['mismatch_indices_at_offset_1']) <= {0},result['mismatch_indices_at_offset_1']
 result['verified_words']=256-len(result['mismatch_indices_at_offset_1'])
 print('PASS: words 1..255 match exactly; word 0 is excluded across the mode/reset transition',flush=True)
finally:
 cy.ir(1023);mv.ir(1023)
 (b.OUT/'result.json').write_text(json.dumps(result,indent=2)+'\n')
 print('Both TAPs BYPASS',flush=True)
