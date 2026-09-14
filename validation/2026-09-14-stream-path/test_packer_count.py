#!/usr/bin/env python3
"""Cycle-equivalence check for forwarding delayed host-packer indices."""
from pathlib import Path
import hashlib, json, subprocess, tempfile
root=Path(__file__).resolve().parents[2]
current=root/'fpga/acq_sram/host_packer.v'
reference=Path(__file__).resolve().parent/'writer-settings/integrated-build/sources/host_packer.v'
bench=r'''
`timescale 1ns/1ps
module tb;
reg clk=0;always #2 clk=~clk;
reg reset=1,word_valid=0,word_bank=0;reg [31:0] word_data=0;
reg [11:0] word_index=0;reg [1:0] bank_done=0;
reg [63:0] first0=0,first1=0;reg [19:0] words0=0,words1=0;
wire push,fault,ref_push,ref_fault;wire [79:0] packet,ref_packet;
sram_host_packer dut(.*);
reference_packer ref_dut(clk,reset,word_valid,word_bank,word_data,word_index,bank_done,first0,first1,words0,words1,ref_push,ref_packet,ref_fault);
integer checks=0,epoch,i,b,n;reg [31:0] rng=32'h8265bac7;
always @(posedge clk)begin
 #0.1;
 if(push!==ref_push || fault!==ref_fault || (push && packet!==ref_packet))$fatal(1,"packer mismatch cycle=%0d",checks);
 checks=checks+1;
end
 task tick;begin @(negedge clk);rng=rng^(rng<<13);rng=rng^(rng>>17);rng=rng^(rng<<5);end endtask
initial begin
 for(epoch=0;epoch<48;epoch=epoch+1)begin
  tick;reset=1;word_valid=0;bank_done=0;
  tick;reset=0;first0={rng,~rng};first1={~rng,rng};
  n=epoch%4==0 ? 2560 : 1+epoch*5;
  for(i=0;i<n;i=i+1)begin
   for(b=0;b<2;b=b+1)begin
    tick;word_valid=1;word_bank=b;word_index=i;word_data=rng;
    if(i%19==0)begin tick;word_valid=0;end
   end
  end
  tick;word_valid=0;words0=n;words1=n;bank_done=3;
  tick;bank_done=0;repeat(12)tick;
  if(fault)$fatal(1,"valid transfer fault");
  // Immediate reuse after metadata drain, plus malformed consecutive offers.
  for(i=0;i<100;i=i+1)begin
   tick;reset=i%23==0;word_valid=rng[0];word_bank=rng[1];word_data=rng;
   word_index=rng[13:2];bank_done=rng[15:14];words0={4'b0,rng[31:16]};words1=rng[19:0];
  end
 end
 tick;reset=0;word_valid=0;bank_done=0;repeat(12)tick;
 $display("PASS packer count forwarding: %0d cycle comparisons, back-to-back banks, full/odd counts, bubbles, invalid offers and reset",checks);$finish;
end
endmodule
'''
with tempfile.TemporaryDirectory(prefix='acq-packer-count-') as directory:
    p=Path(directory);old=reference.read_bytes();new=current.read_bytes()
    assert hashlib.sha256(old).hexdigest()=='1b50bb8cdc04ab693353f8311c034742a1109e16ea8f66471bb0dede850cd883'
    (p/'ref.v').write_text(old.decode().replace('module sram_host_packer(', 'module reference_packer('))
    (p/'current.v').write_bytes(new);(p/'tb.v').write_text(bench)
    print(json.dumps({'current':hashlib.sha256(new).hexdigest(),'reference':hashlib.sha256(old).hexdigest(),'bench':hashlib.sha256(bench.encode()).hexdigest()}),flush=True)
    subprocess.run(['iverilog','-g2012','-s','tb','-o',str(p/'test'),str(p/'ref.v'),str(p/'current.v'),str(p/'tb.v')],check=True)
    subprocess.run(['vvp',str(p/'test')],check=True,timeout=60)
