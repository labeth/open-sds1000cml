#!/usr/bin/env python3
"""ADC startup chronology with newly enabled encode clocks and an ideal FIFO."""
from pathlib import Path
import hashlib,json,subprocess,tempfile
root=Path(__file__).resolve().parents[2]
fifo='''
module dcfifo #(parameter lpm_width=80,lpm_numwords=32,lpm_widthu=5,
 lpm_showahead="ON",add_ram_output_register="ON",overflow_checking="ON",underflow_checking="ON",
 rdsync_delaypipe=4,wrsync_delaypipe=4,read_aclr_synch="ON",write_aclr_synch="ON",use_eab="ON",intended_device_family="Cyclone IV E")(
 input aclr,wrclk,input [lpm_width-1:0] data,input wrreq,output wrfull,
 input rdclk,rdreq,output [lpm_width-1:0] q,output rdempty,output [lpm_widthu-1:0] rdusedw);
 reg [lpm_width-1:0] mem[0:lpm_numwords-1];integer wp=0,rp=0;
 assign wrfull=wp-rp>=lpm_numwords;assign rdempty=wp==rp;assign q=mem[rp%lpm_numwords];assign rdusedw=wp-rp;
 always @(posedge wrclk or posedge aclr)if(aclr)wp<=0;else if(wrreq && !wrfull)begin mem[wp%lpm_numwords]<=data;wp<=wp+1;end
 always @(posedge rdclk or posedge aclr)if(aclr)rp<=0;else if(rdreq && !rdempty)rp<=rp+1;
endmodule
'''
with tempfile.TemporaryDirectory(prefix='acq-encode-start-') as directory:
 p=Path(directory);names=['fpga/acq_sram/interleave.v','fpga/common/ddio_pair.v','fpga/common/lane_in.v','fpga/default/lanemap_seed.vh'];files=[];hashes={}
 for name in names:
  data=(root/name).read_bytes();dest=p/Path(name).name;dest.write_bytes(data);hashes[name]=hashlib.sha256(data).hexdigest()
  if name.endswith('.v'):files.append(str(dest))
 bench=(root/'fpga/acq_sram/sim/tb_adc_interleave.v').read_text().replace('adc_interleave dut(', 'adc_interleave #(.SYNC_ENCODE(1)) dut(').replace("lane,10'h3ff,",'lane,mask,').replace(' wire [79:0] lane;', ' reg [9:0] mask=0;\n wire [79:0] lane;').replace('#200;enable=1;',"#200;mask=10'h3ff;enable=1;")
 bench=bench.replace(' integer count=0,last=-1,i;', ' integer count=0,last=-1,i,bit_index,sample_index,pair_index;reg expected_p,expected_n;')
 bench=bench.replace("#200;mask=10'h3ff;enable=1;", """#100;
  for(bit_index=0;bit_index<10;bit_index=bit_index+1)begin
   mask=10'b1<<bit_index;#50.013; // Observe away from DDR edge delta cycles.
   for(sample_index=0;sample_index<20;sample_index=sample_index+1)begin
    #1.1;
    for(pair_index=0;pair_index<5;pair_index=pair_index+1)begin
     expected_p=mask[2*pair_index] && ((pair_index==0 || pair_index==1 || pair_index==3) ? dut.phase[pair_index] : !dut.phase[pair_index]);
     expected_n=mask[2*pair_index+1] && ((pair_index==0 || pair_index==1 || pair_index==3) ? !dut.phase[pair_index] : dut.phase[pair_index]);
     if(ep[pair_index]!==expected_p || en[pair_index]!==expected_n)$fatal(1,"encode mask/polarity bit=%0d pair=%0d",bit_index,pair_index);
    end
   end
  end
  $display("PASS all ten independent encode enables and phase polarities");
  mask=0;#200;mask=10'h3ff;enable=1;""")
 (p/'tb.v').write_text(fifo+bench);hashes['generated_bench']=hashlib.sha256((fifo+bench).encode()).hexdigest();print(json.dumps(hashes),flush=True)
 subprocess.run(['iverilog','-g2012','-DSIM','-I',str(p),'-s','tb','-o',str(p/'test'),*files,str(p/'tb.v')],check=True)
 subprocess.run(['vvp',str(p/'test')],check=True,timeout=30)
