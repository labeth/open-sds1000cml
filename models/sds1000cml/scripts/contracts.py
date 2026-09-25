#!/usr/bin/env python3
"""Index pinned pin maps, PLL declarations and source register tables.
No build script is executed and no hardware is contacted.
"""
import json,re
from inventory import ROOT,REV,read,write
files=json.loads((ROOT/'evidence/source-inventory.json').read_text());known={x['path']:x for x in files}
base_path='fpga/default/default.qsf';base=read(base_path)
base_pins={port:ball for ball,port in re.findall(r'set_location_assignment PIN_(\w+) -to (\S+)',base)}
profile_ports=('clk','mclk_in','nCS1','nOE','nWE','gpmc_a2','gpmc_b1','k1','k2','g1','g2','d1','d2','f1','f2','j2','a11')
profiles=[]
for path in ['fpga/acq_sram/build.py','fpga/acq_sram/build_profile.py']:
 text=read(path)
 m=re.search(r"enumerate\('([A-Z0-9 ]+)'\.split\(\)\)",text)
 assert m,path+' SRAM pin literal not found'
 balls=m.group(1).split();assert len(balls)==32
 pins={p:b for p,b in base_pins.items() if p.startswith(('gpmc_d[','sel[','enc_p[','enc_n[','lane[')) or p in profile_ports}
 pins.update({f'dq[{i}]':ball for i,ball in enumerate(balls)})
 assert len(pins)==160 and len(set(pins.values()))==160,(path,len(pins))
 profiles.append(dict(source=path,gitBlob=known[path]['gitBlob'],base=base_path,pins=[dict(port=p,ball=b) for p,b in sorted(pins.items())],evidenceKind='reconstructed from the committed builder pin subset plus explicit DQ override',ioStandard='3.3-V LVTTL',sramDQDrive='8MA',qualified=False))
clocks=[]
for row in files:
 path=row['path']
 if not path.startswith('fpga/') or not path.endswith('.v') or '/sim/' in path:continue
 text=read(path)
 if 'altpll' not in text:continue
 ref=re.search(r'\.inclk0_input_frequency\((\d+)\)',text)
 out=[]
 for idx,mult in re.findall(r'\.clk(\d)_multiply_by\((\d+)\)',text):
  div=re.search(r'\.clk'+idx+r'_divide_by\((\d+)\)',text);phase=re.search(r'\.clk'+idx+r'_phase_shift\("([^"]+)"\)',text)
  if not div:continue
  x=dict(index=int(idx),multiply=int(mult),divide=int(div.group(1)),phasePicoseconds=phase.group(1) if phase else 'unspecified')
  if ref:x['frequencyHz']=1e12/int(ref.group(1))*int(mult)/int(div.group(1))
  out.append(x)
 clocks.append(dict(source=path,gitBlob=row['gitBlob'],referencePeriodPicoseconds=int(ref.group(1)) if ref else None,outputs=out,limits='PLL declarations only; no independent timing/CDC/board qualification inferred.'))
tables=[]
for path,status in [('fpga/default/docs/REGISTER-MAP.md','generated documentation; current codegen drift fails'),('specs/12-acquisition-fpga.md','specification with historical and unresolved physical claims'),('fpga/acq_sram/STREAMING.md','legacy revision-11 experimental documentation'),('docs/fpga-profile-command-abi.md','draft new-profile ABI; some board-status prose is stale')]:
 heading='';table=None
 for i,line in enumerate(read(path).splitlines(),1):
  if line.startswith('#'):heading=line.lstrip('# ')
  if line.startswith('|'):
   if table is None:table=dict(source=path,gitBlob=known[path]['gitBlob'],heading=heading,line=i,status=status,rows=[]);tables.append(table)
   table['rows'].append([s.strip() for s in line.strip('|').split('|')])
  else:table=None
write('hardware-contracts.json',dict(commit=REV,profilePinMaps=profiles,pllDeclarations=clocks,constraintFiles=[dict(path=x['path'],gitBlob=x['gitBlob']) for x in files if x['path'].startswith('fpga/') and x['path'].endswith('.sdc')],registerAndSpecificationTables=tables))
print(f'PASS: two distinct 160-pin maps; {len(clocks)} PLL source declarations; {len(tables)} pinned specification tables')
