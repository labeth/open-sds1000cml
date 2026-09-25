// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-PRECISION
`timescale 1ns/1ps
// Two samples per channel per 250 MHz cycle. Each integrator's pair sum is
// registered before the feedback adder, keeping one carry chain per cycle.
// TRLC-LINKS: REQ-SDS-040
module cic_pair_integrator #(parameter W=28)(input clk,enable,valid,input signed [W-1:0] a,b,
 output reg out_valid=0,output reg signed [W-1:0] lo=0,hi=0);
 (* preserve, dont_merge *) reg local_enable=0;
 always @(posedge clk)local_enable<=enable;
 localparam L=W/2,H=W-L;
 reg pending=0,pending_hi=0;
 reg signed [W-1:0] first=0,sum=0;
 reg [L-1:0] state_lo=0,lo_first=0,lo_sum=0;
 reg [H-1:0] state_hi=0,hi_first=0,hi_sum=0;
 reg carry_first=0,carry_sum=0;
 wire [L:0] next_first={1'b0,state_lo}+{1'b0,first[L-1:0]};
 wire [L:0] next_sum={1'b0,state_lo}+{1'b0,sum[L-1:0]};
 wire [H-1:0] next_hi_first=state_hi+hi_first+carry_first;
 wire [H-1:0] next_hi_sum=state_hi+hi_sum+carry_sum;
 // The high accumulator trails the low accumulator by one accepted token.
 // Pipeline its operand and low carry together; stalls preserve alignment.
 always @(posedge clk)begin
  pending<=local_enable && valid;
  pending_hi<=local_enable && pending;
  out_valid<=local_enable && pending_hi;
  if(valid)begin first<=a;sum<=a+b;end
  if(pending)begin
   state_lo<=next_sum[L-1:0];
   lo_first<=next_first[L-1:0];lo_sum<=next_sum[L-1:0];
   carry_first<=next_first[L];carry_sum<=next_sum[L];
   hi_first<=first[W-1:L];hi_sum<=sum[W-1:L];
  end
  if(pending_hi)begin
   state_hi<=next_hi_sum;
   lo<={next_hi_first,lo_first};hi<={next_hi_sum,lo_sum};
  end
  if(!local_enable)begin
   pending<=0;pending_hi<=0;out_valid<=0;
   state_lo<=0;state_hi<=0;lo<=0;hi<=0;
  end
 end
endmodule

// CIC3 /16 over signed 8-bit ADC integers, with 12 guard bits for 16^3.
// The former 28-bit Q8 arithmetic had eight identically zero low bits.
// Removing them preserves modulo arithmetic exactly; shifting by four at
// output instead of twelve restores the same signed Q8 result and latency.
// TRLC-LINKS: REQ-SDS-040
module cic16_pair(input clk,enable,valid,input [7:0] a,b,output out_valid,output [15:0] q);
 wire signed [19:0] x0=$signed(a^8'h80),x1=$signed(b^8'h80);
 wire v1,v2,v3;wire signed [19:0] a1,b1,a2,b2,a3,b3;
 cic_pair_integrator #(.W(20)) i1(clk,enable,valid,x0,x1,v1,a1,b1);
 cic_pair_integrator #(.W(20)) i2(clk,enable,v1,a1,b1,v2,a2,b2);
 cic_pair_integrator #(.W(20)) i3(clk,enable,v2,a2,b2,v3,a3,b3);
 (* preserve, dont_merge *) reg comb_enable=0;
 always @(posedge clk)comb_enable<=enable;
 reg [2:0] count=0;reg [5:0] cv=0;reg [3:0] warm=0;
 reg signed [19:0] z1=0,z2=0,z3=0,c1=0,c2=0,c3=0;
 reg [10:0] low1=0,low2=0,low3=0;
 reg [9:0] high1=0,high2=0,high3=0;
 // Each sparse comb subtraction takes two short arithmetic stages. The
 // valid token follows all six stages; /16 produces at most one per eight
 // core clocks, so the pipeline retains full throughput with unchanged values.
 always @(posedge clk)begin
  cv<={cv[4:0],v3 && count==7};
  if(v3)begin
   count<=count+1'b1;
   if(count==7)begin
    low1<={1'b0,b3[9:0]}-{1'b0,z1[9:0]};high1<=b3[19:10]-z1[19:10];z1<=b3;
   end
  end
  if(cv[0])c1<={high1-low1[10],low1[9:0]};
  if(cv[1])begin low2<={1'b0,c1[9:0]}-{1'b0,z2[9:0]};high2<=c1[19:10]-z2[19:10];z2<=c1;end
  if(cv[2])c2<={high2-low2[10],low2[9:0]};
  if(cv[3])begin low3<={1'b0,c2[9:0]}-{1'b0,z3[9:0]};high3<=c2[19:10]-z3[19:10];z3<=c2;end
  if(cv[4])c3<={high3-low3[10],low3[9:0]};
  if(cv[5] && warm<8)warm<=warm+1'b1;
  if(!comb_enable)begin count<=0;cv<=0;warm<=0;z1<=0;z2<=0;z3<=0;c1<=0;c2<=0;c3<=0;end
 end
 assign q={~c3[19],c3[18:4]};assign out_valid=enable && cv[5] && warm==8;
endmodule

// Further CIC3 /2, /4, /8 or /16 stages run at 100 MHz with sparse valid
// tokens. A bypass preserves Q8 precision and adds one clock of latency.
// TRLC-LINKS: REQ-SDS-040
module cic_stage(input clk,enable,valid,input [3:0] factor_log,input [15:0] data,
 output reg out_valid=0,output reg [15:0] q=0);
 reg signed [27:0] i1=0,i2=0,i3=0,z1=0,z2=0,z3=0,c1=0,c2=0,c3=0;
 wire signed [27:0] x=$signed(data^16'h8000);
 reg [3:0] count=0,warm=0;reg [2:0] cv=0;reg [15:0] bypass=0;reg bv=0;
 wire [3:0] last=(5'd1<<factor_log)-1'b1;
 wire signed [27:0] normalized=c3 >>> (factor_log*3);
 always @(posedge clk)begin
  bv<=valid;bypass<=data;cv<={cv[1:0],valid && count==last};
  if(valid)begin
   i1<=i1+x;i2<=i2+i1;i3<=i3+i2;
   if(count==last)begin count<=0;c1<=i3-z1;z1<=i3;end else count<=count+1'b1;
  end
  if(cv[0])begin c2<=c1-z2;z2<=c1;end
  if(cv[1])begin c3<=c2-z3;z3<=c2;end
  if(cv[2] && warm<8)warm<=warm+1'b1;
  if(factor_log==0)begin q<=bypass;out_valid<=bv;end
  else begin q<=normalized[15:0]^16'h8000;out_valid<=cv[2] && warm==8;end
  if(!enable)begin i1<=0;i2<=0;i3<=0;z1<=0;z2<=0;z3<=0;c1<=0;c2<=0;c3<=0;count<=0;warm<=0;cv<=0;bv<=0;out_valid<=0;end
 end
endmodule

// TRLC-LINKS: REQ-SDS-040
module adc_precision #(parameter SHARED_TAIL=0)(input core,packclk,clk100,enable,input [4:0] decim_log,input [31:0] raw,input raw_valid,
 output reg [31:0] data=0,output reg valid=0,output reg fault=0);
 wire [15:0] first0,first1;wire fv0,fv1;
 cic16_pair first_ch1(core,enable,raw_valid,raw[7:0],raw[23:16],fv0,first0);
 cic16_pair first_ch2(core,enable,raw_valid,raw[15:8],raw[31:24],fv1,first1);
 reg [31:0] in_mail=0;reg in_toggle=0;
 // Register the CIC valid decode before the mailbox's wide write enable.
 reg [31:0] first_pair=0;reg first_valid=0;
 always @(posedge core)begin
  first_pair<={first1,first0};first_valid<=enable && fv0;
  if(first_valid)begin in_mail<=first_pair;in_toggle<=!in_toggle;end
 end
 reg in_toggle_s=0,in_seen=0;reg [2:0] pack_enable=0;
 always @(posedge packclk)begin in_toggle_s<=in_toggle;in_seen<=in_toggle_s;end
 wire in_push=in_toggle_s!=in_seen && pack_enable[2];
 wire in_empty,in_full;wire [31:0] in_q;
 reg [2:0] enable_s=0;
 wire [4:0] filter_decim;
 generate if(SHARED_TAIL)begin:config_domain
  // Disable may be shorter than a slower clock period. Assert reset
  // immediately, then hold it through three destination-clock release stages.
  always @(posedge clk100 or negedge enable)
   if(!enable)enable_s<=0;else enable_s<={enable_s[1:0],1'b1};
  always @(posedge packclk or negedge enable)
   if(!enable)pack_enable<=0;else pack_enable<={pack_enable[1:0],1'b1};
  // The caller holds decim_log before enable and throughout the epoch.
  // Refresh during reset; the final capture precedes the first enabled
  // filter edge by one full clk100 cycle. The first two release clocks
  // provide settling time for the bounded core-to-clk100 configuration bus.
  (* preserve, dont_merge *) reg [4:0] decim_100=0;
  always @(posedge clk100)if(!enable_s[2])decim_100<=decim_log;
  assign filter_decim=decim_100;
 end else begin:legacy_enable
  always @(posedge clk100)enable_s<={enable_s[1:0],enable};
  always @(posedge packclk)pack_enable<={pack_enable[1:0],enable};
  // Latch before the synchronized enable reaches the filter. Configuration
  // remains fixed throughout an acquisition, like the shared-tail path.
  (* preserve, dont_merge *) reg [4:0] decim_100=0;
  always @(posedge clk100)if(!enable_s[2])decim_100<=decim_log;
  assign filter_decim=decim_100;
 end endgenerate
 dcfifo #(.lpm_width(32),.lpm_numwords(32),.lpm_widthu(5),.lpm_showahead("ON"),.add_ram_output_register("ON"),
 .rdsync_delaypipe(4),.wrsync_delaypipe(4),.read_aclr_synch("ON"),.write_aclr_synch("ON"),.use_eab("ON"),.intended_device_family("Cyclone IV E")) input_queue(
 .aclr(!enable),.wrclk(packclk),.data(in_mail),.wrreq(in_push && !in_full),.wrfull(in_full),
 .rdclk(clk100),.rdreq(!in_empty && enable_s[2]),.q(in_q),.rdempty(in_empty));
 wire [31:0] stage_data[0:4];wire [4:0] stage_valid;
 assign stage_data[0]=in_q;assign stage_valid[0]=!in_empty && enable_s[2];
 genvar s;generate for(s=0;s<(SHARED_TAIL ? 1 : 4);s=s+1)begin:stage
  wire [4:0] remain=filter_decim>4+4*s ? filter_decim-(4+4*s) : 5'd0;
  wire [3:0] log=remain>4 ? 4'd4 : remain[3:0];wire unused_valid;
  cic_stage c1(clk100,enable_s[2],stage_valid[s],log,stage_data[s][15:0],stage_valid[s+1],stage_data[s+1][15:0]);
  cic_stage c2(clk100,enable_s[2],stage_valid[s],log,stage_data[s][31:16],unused_valid,stage_data[s+1][31:16]);
 end endgenerate
 wire tail_fault;
 generate if(SHARED_TAIL)begin:shared_tail
  wire [3:0] remaining_log=filter_decim>8 ? filter_decim-8 : 0;
  cic_precision_tail tail(.clk(clk100),.enable(enable_s[2]),.valid(stage_valid[1]),
   .remaining_log(remaining_log),.data(stage_data[1]),.ready(),
   .out_valid(stage_valid[4]),.q(stage_data[4]),.fault(tail_fault));
 end else begin:parallel_tail
  assign tail_fault=1'b0;
 end endgenerate
 wire out_empty,out_full;wire [31:0] out_q;reg overflow=0,in_overflow=0;reg [2:0] overflow_s=0,in_overflow_s=0;
 always @(posedge packclk)if(!pack_enable[2])in_overflow<=0;else if(in_push && in_full)in_overflow<=1;
 always @(posedge clk100)if(!enable_s[2])overflow<=0;else if((stage_valid[4] && out_full) || tail_fault)overflow<=1;
 dcfifo #(.lpm_width(32),.lpm_numwords(32),.lpm_widthu(5),.lpm_showahead("ON"),.add_ram_output_register("ON"),
 .rdsync_delaypipe(4),.wrsync_delaypipe(4),.read_aclr_synch("ON"),.write_aclr_synch("ON"),.use_eab("ON"),.intended_device_family("Cyclone IV E")) output_queue(
 .aclr(!enable),.wrclk(clk100),.data(stage_data[4]),.wrreq(stage_valid[4] && !out_full),.wrfull(out_full),
 .rdclk(packclk),.rdreq(!out_empty && pack_enable[2]),.q(out_q),.rdempty(out_empty));
 reg [31:0] out_mail=0;reg out_toggle=0;
 always @(posedge packclk)if(!out_empty && pack_enable[2])begin out_mail<=out_q;out_toggle<=!out_toggle;end
 reg out_toggle_d=0,out_seen=0;
 always @(posedge core)begin
  out_toggle_d<=out_toggle;out_seen<=out_toggle_d;
  valid<=enable && out_toggle_d!=out_seen;
  // Payload is consumed only with valid; continuously register the held mailbox.
  data<=out_mail;
 end
 always @(posedge core)begin
  overflow_s<={overflow_s[1:0],overflow};in_overflow_s<={in_overflow_s[1:0],in_overflow};
  if(!enable)begin fault<=0;overflow_s<=0;in_overflow_s<=0;end
  else if(in_overflow_s[2] || overflow_s[2])fault<=1;
 end
endmodule
