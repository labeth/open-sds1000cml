#!/usr/bin/env python3
"""Compare shared acceptance controls with the frozen pre-change host sink."""
from pathlib import Path
import hashlib, json, subprocess, tempfile
root = Path(__file__).resolve().parents[2]
source = root / 'fpga/acq_sram/host_sink.v'
reference = Path(__file__).resolve().parent / 'trigger-pipeline/first-build/sources/host_sink.v'
bench = r'''
`timescale 1ns/1ps
module tb;
reg clk=0; always #4 clk=~clk;
reg reset=1,upstream_fault=0,valid=0,ram_fault=0;
reg [79:0] data=0;
wire ready,ram_write,fault; wire [11:0] ram_pair,words0,words1;
wire [63:0] ram_data,first0,first1; wire [1:0] publish;
wire rr,rw,rf; wire [11:0] rp,w0,w1;wire [63:0] rd,f0,f1;wire [1:0] pub;
sram_host_sink dut(.*);
reference_sink ref_dut(clk,reset,upstream_fault,valid,data,rr,rw,rp,rd,ram_fault,rf,pub,f0,f1,w0,w1);
integer checks=0,epoch,i,n,b;reg [31:0] rng=32'hfac3a569;
always @(posedge clk)begin
 #0.1;
 if({ready,ram_write,ram_pair,ram_data,fault,publish,first0,first1,words0,words1,dut.pairs0,dut.pairs1} !==
    {rr,rw,rp,rd,rf,pub,f0,f1,w0,w1,ref_dut.pairs0,ref_dut.pairs1})$fatal(1,"sink mismatch at %0d",checks);
 checks=checks+1;
end
 task tick;begin @(negedge clk);rng=rng^(rng<<13);rng=rng^(rng>>17);rng=rng^(rng<<5);end endtask
initial begin
 for(epoch=0;epoch<48;epoch=epoch+1)begin
  tick;reset=1;valid=0;ram_fault=0;upstream_fault=0;
  tick;reset=0;
  n=epoch%4==0 ? 1280 : 1+epoch*7;
  for(i=0;i<n;i=i+1)begin
   for(b=0;b<2;b=b+1)begin
    tick;valid=1;data={1'b0,1'(b),14'(b*1280+i),rng,~rng};
    if(i%7==0)begin tick;valid=0;end
   end
  end
  for(b=0;b<2;b=b+1)begin
   tick;valid=1;data={1'b1,1'(b),14'(2*n-(epoch%2)),rng,~rng};
  end
  tick;valid=0;
  // Bad pair, descriptor, backend faults, and attempts after sticky fault.
  for(i=0;i<100;i=i+1)begin
   tick;data={rng[15:0],rng,~rng};valid=rng[3];
   reset=i%17==0;ram_fault=i%29==0;upstream_fault=i%31==0;
  end
 end
 tick;valid=0;reset=0;ram_fault=0;upstream_fault=0;repeat(3)tick;
 $display("PASS host sink equivalence: %0d cycles, both banks, full/odd descriptors, bubbles, malformed packets and faults",checks);$finish;
end
endmodule
'''
with tempfile.TemporaryDirectory(prefix='acq-sink-') as directory:
    dest=Path(directory)
    old=reference.read_bytes()
    assert hashlib.sha256(old).hexdigest() == '8334e289686c3c9bd328137e302947676981d7b66bbf5e08df1d0e8993972e55'
    (dest/'reference.v').write_text(old.decode().replace('module sram_host_sink(', 'module reference_sink('))
    (dest/'current.v').write_bytes(source.read_bytes())
    (dest/'tb.v').write_text(bench)
    print(json.dumps({'current':hashlib.sha256(source.read_bytes()).hexdigest(), 'reference':hashlib.sha256(old).hexdigest(), 'bench':hashlib.sha256(bench.encode()).hexdigest()}), flush=True)
    subprocess.run(['iverilog','-g2012','-s','tb','-o',str(dest/'test'),str(dest/'current.v'),str(dest/'reference.v'),str(dest/'tb.v')],check=True)
    subprocess.run(['vvp',str(dest/'test')],check=True,timeout=60)
