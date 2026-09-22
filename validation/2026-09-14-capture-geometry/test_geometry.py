#!/usr/bin/env python3
"""Compare actual RTL geometry predicates against full-width arithmetic."""
from pathlib import Path
import subprocess,tempfile
root=Path(__file__).resolve().parents[2]
with tempfile.TemporaryDirectory(prefix='acq-geometry-') as tmp:
 p=Path(tmp)
 for name in ('acquisition_path.v','finite_writer.v'):
  s=(root/'fpga/acq_sram'/name).read_text()
  block=s[s.index(' wire capture_geometry;'):s.index(' end endgenerate',s.index(' wire capture_geometry;'))+len(' end endgenerate')]
  for aw in (4,8,9,13,19):
   tb='''module tb;
 localparam AW=WIDTH; reg [AW:0] pre_count=0,post_count=0;
 integer i,j; reg [AW+1:0] total;
 BLOCK
 task check;
 begin #1;total={1'b0,pre_count}+{1'b0,post_count};
 if(capture_geometry !== (post_count!=0 && total<=(1<<AW)))
 $fatal(1,"geometry mismatch AW=%0d pre=%0d post=%0d",AW,pre_count,post_count);
 end endtask
 initial begin
 if(AW<=9)begin
 for(i=0;i<(1<<(AW+1));i=i+1)for(j=0;j<(1<<(AW+1));j=j+1)begin
 pre_count=i;post_count=j;check;end
 end else begin
 for(i=0;i<100000;i=i+1)begin pre_count=$urandom;post_count=$urandom;check;end
 for(i=0;i<=(1<<AW);i=i+1)begin
 pre_count=i;
 for(j=-1;j<=1;j=j+1)begin post_count=(1<<AW)-i+j;check;end
 end
 end
 $display("PASS AW=%0d exhaustive small / random and every full-depth boundary",AW);$finish;
 end
 endmodule
'''.replace('WIDTH',str(aw)).replace('BLOCK',block)
   (p/'tb.v').write_text(tb)
   subprocess.run(['iverilog','-g2012','-s','tb','-o',str(p/'test'),str(p/'tb.v')],check=True)
   print(name,flush=True)
   subprocess.run(['vvp',str(p/'test')],check=True,timeout=60)
