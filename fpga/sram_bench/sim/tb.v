`timescale 1ns/1ps
module bench_pll(input refclk,output c0,c1,locked);
assign c0=refclk;assign c1=refclk;assign locked=1;
endmodule
module altddio_out #(parameter width=1,power_up_high="OFF",intended_device_family="Cyclone IV E")
(input outclock,datain_h,datain_l,oe,aclr,aset,sclr,sset,outclocken,output dataout);
// Match altddio_out: both inputs latch at the rising edge.
reg low_latched=0,output_value=0;
always @(posedge outclock or posedge aclr) begin
 if(aclr) begin low_latched<=0;output_value<=0;end
 else if(outclocken) begin low_latched<=datain_l;output_value<=datain_h;end
end
always @(negedge outclock) if(!aclr && outclocken) output_value<=low_latched;
assign dataout=output_value;
endmodule
module tb;
reg clk=0,refclk=0;always #6.25 clk=~clk;always #50 refclk=~refclk;
wire [15:0] gd;wire [31:0] dq;wire k1,k2,g1;
sram_bench_top dut(.clk(clk),.mclk_in(refclk),.nCS1(1'b1),.nOE(1'b1),.nWE(1'b1),.sel(5'b0),.gpmc_a2(1'b0),.gpmc_b1(1'b0),.gpmc_d(gd),.dq(dq),.k1(k1),.k2(k2),.g1(g1));
integer alias_test=0; initial alias_test=$test$plusargs("alias");
reg [31:0] mem[0:524287];reg [18:0] address=0;
wire [18:0] physical_address=alias_test ? {1'b0,address[17:0]} : address;
reg [31:0] stage1=0,stage2=0;integer writes=0,reads=0;
assign dq=(k1&&g1)?stage2:32'bz;
always @(posedge k2) if(g1) begin
 if(!k1) begin mem[physical_address]<=dq;writes=writes+1;end
 else begin stage1<=mem[physical_address];stage2<=stage1;reads=reads+1;end
 address<=address+1;
end
initial begin
 #200;dut.req=1;
 wait(dut.state[dut.DONE]);#1;
 $display("writes=%0d reads=%0d checked=%0d errors=%0d first=%0d want=%h got=%h",writes,reads,dut.checked,dut.errors,dut.first_index,dut.first_want,dut.first_got);
 if(writes!=524304||reads!=524320||dut.checked!=524288)$fatal(1,"BIST sequencing failure");
 if(alias_test) begin if(dut.errors==0)$fatal(1,"failed to detect address alias");end
 else if(dut.errors!=0)$fatal(1,"unexpected memory mismatch");
 $display("PASS behavioral SRAM, alias test=%0d",alias_test);$finish;
end
initial begin #120000000;$fatal(1,"timeout");end
endmodule
