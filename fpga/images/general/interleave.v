// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-INTERLEAVE
`timescale 1ns/1ps
`include "lanemap_seed.vh"
// Factory-derived five 100 MHz phases; 80 input bits per 10 ns.
// Independent encode-stop/DC checks confirm the clock/core connections.
// AC aperture skew and gain/offset matching still require qualification.
// TRLC-LINKS: REQ-SDS-039, REQ-SDS-058
module adc_interleave #(parameter SYNC_ENCODE=0)(
 input refclk,memclk,packclk,enable,input [79:0] lane,input [9:0] encode_enable,
 input snapshot_request,output snapshot_ack,input consume,output [31:0] word_data,output valid,output reg fault=0,
 output [79:0] snapshot,output [4:0] enc_p,enc_n,output locked
);
 wire [4:0] phase;adc_phase_pll clocks(refclk,phase,locked);
 wire [79:0] converted;
 // Capture five ns after each factory base phase. Encode phases stay fixed;
 // this input sampling point is being qualified against midscale bit errors.
 wire [4:0] capture_phase=~phase;
 genvar p,b;
 generate for(p=0;p<5;p=p+1)begin:pair
  // PLL ports are in measured pair order: 3,4,2,0,1 ns.
  localparam POS=(p==0 || p==1 || p==3);
  wire [1:0] encode_local;
  if(SYNC_ENCODE)begin:synchronized_encode
   // Each converter enable is independent. Settings stay fixed throughout
   // capture; the local stages settle before the frontend warm-up completes.
   (* async_reg="true",preserve *) reg [1:0] meta=0,settled=0;
   always @(posedge phase[p])begin
    meta<=encode_enable[2*p+:2];settled<=meta;
   end
   assign encode_local=settled;
  end else begin:legacy_encode
   assign encode_local=encode_enable[2*p+:2];
  end
  ddio_out1 ep(.outclock(phase[p]),.dh(POS && locked && encode_local[0]),.dl(!POS && locked && encode_local[0]),.pad(enc_p[p]));
  ddio_out1 en(.outclock(phase[p]),.dh(!POS && locked && encode_local[1]),.dl(POS && locked && encode_local[1]),.pad(enc_n[p]));
  wire [15:0] raw;
  for(b=0;b<16;b=b+1)begin:bit_in
   assign raw[b]=lane[`LANEMAP_ENTRY(16*p+b)];
  end
  lane_in #(.N(16)) inputs(.clk(capture_phase[p]),.pad(raw),.q(converted[16*p+:16]));
 end endgenerate
 reg [79:0] frame=0;
 // Gather at +2 ns, after every +5 ns input phase of the preceding period.
 always @(posedge phase[2]) frame<=converted;
 (* async_reg = "true" *) reg [2:0] snap_req_s=0;
 reg snap_ack=0;reg [79:0] snap_payload=0;
 (* async_reg = "true" *) reg [2:0] snap_ack_s=0;
 always @(posedge phase[2])begin
  snap_req_s<={snap_req_s[1:0],snapshot_request};
  if(snap_req_s[2]!=snap_ack)begin snap_payload<=frame;snap_ack<=snap_req_s[2];end
 end
 always @(posedge packclk)snap_ack_s<={snap_ack_s[1:0],snap_ack};
 assign snapshot_ack=snap_ack_s[2];assign snapshot=snap_payload;
 // With synchronized encode settings, allow seven phase clocks for the
 // enables, DDR output, converter propagation and two-stage lane/frame gather
 // before retaining frames. Disable immediately abandons this warm-up epoch.
 localparam ENABLE_STAGES=SYNC_ENCODE ? 7 : 3;
 (* async_reg = "true" *) reg [ENABLE_STAGES-1:0] enable_s=0;
 generate if(SYNC_ENCODE)begin:encode_warmup
  always @(posedge phase[2] or negedge enable)
   if(!enable)enable_s<=0;else enable_s<={enable_s[ENABLE_STAGES-2:0],1'b1};
 end else begin:legacy_warmup
  always @(posedge phase[2])enable_s<={enable_s[1:0],enable};
 end endgenerate
 wire [79:0] chronological={frame[31:24],frame[7:0],frame[47:40],frame[71:64],frame[63:56],frame[23:16],frame[15:8],frame[39:32],frame[79:72],frame[55:48]};
 wire empty,full;wire [4:0] used;wire [79:0] q;wire pop;
 // One shallow FIFO separates ADC phase alignment from the 250 MHz SRAM.
 dcfifo #(.lpm_width(80),.lpm_numwords(32),.lpm_widthu(5),.lpm_showahead("ON"),
 .add_ram_output_register("ON"),.overflow_checking("ON"),.underflow_checking("ON"),.use_eab("ON"),
 .rdsync_delaypipe(4),.wrsync_delaypipe(4),.read_aclr_synch("ON"),.write_aclr_synch("ON"),
 .intended_device_family("Cyclone IV E")) queue(
 .aclr(!enable || !locked),.wrclk(phase[2]),.data(chronological),.wrreq(enable_s[ENABLE_STAGES-1] && !full),.wrfull(full),
 .rdclk(packclk),.rdreq(pop),.q(q),.rdempty(empty),.rdusedw(used));
 reg overflow=0;reg [2:0] overflow_s=0;
 always @(posedge phase[2]) if(!enable_s[ENABLE_STAGES-1])overflow<=0;else if(full)overflow<=1;
 wire pack_enable,pack_consume;
 generate if(SYNC_ENCODE)begin:pack_control
  (* async_reg="true",preserve *) reg [2:0] enable_s=0;
  (* async_reg="true",preserve *) reg [1:0] consume_s=0;
  // Disable abandons the pack epoch immediately; release only on local clocks.
  always @(posedge packclk or negedge enable)begin
   if(!enable)enable_s<=0;
   else enable_s<={enable_s[1:0],1'b1};
  end
  always @(posedge packclk or negedge pack_enable)
   if(!pack_enable)consume_s<=0;else consume_s<={consume_s[0],consume};
  assign pack_enable=enable_s[2];assign pack_consume=consume_s[1];
 end else begin:legacy_pack_control
  assign pack_enable=enable;assign pack_consume=consume;
 end endgenerate
 reg consume_q=0;
 always @(posedge packclk)consume_q<=pack_enable && pack_consume;
 wire [63:0] wide_raw;wire wide_raw_valid,pack_fault;
 interleave_gearbox64 pack(.clk(packclk),.enable(pack_enable),.q(q),.empty(empty),.used(used),.consume(consume_q),.word_data(wide_raw),.valid(wide_raw_valid),.pop(pop),.fault(pack_fault));
 reg [63:0] wide_word=0;reg wide_valid=0,wide_toggle=0;
 generate if(SYNC_ENCODE)begin:mailbox_epoch
  // A disable shorter than one pack period must invalidate the held mailbox
  // before core consumption resumes. Payload itself needs no reset.
  always @(posedge packclk or negedge pack_enable)
   if(!pack_enable)wide_valid<=0;else wide_valid<=wide_raw_valid && consume_q;
 end else begin:legacy_mailbox_epoch
  always @(posedge packclk)wide_valid<=pack_enable && wide_raw_valid && consume_q;
 end endgenerate
 always @(posedge packclk)begin
  wide_word<=wide_raw;wide_toggle<=!wide_toggle;
  overflow_s<={overflow_s[1:0],overflow};
  if(!pack_enable)begin fault<=0;overflow_s<=0;end
  else if(overflow_s[2] || pack_fault)fault<=1;
 end
 reg toggle_d=0,toggle_seen=0;reg [31:0] serial_word=0,serial_high=0;(* preserve, dont_merge *) reg serial_valid=0;reg high_valid=0;
 always @(posedge memclk)begin
  toggle_d<=wide_toggle;toggle_seen<=toggle_d;
  if(toggle_d!=toggle_seen)begin
   serial_word<=wide_word[31:0];serial_high<=wide_word[63:32];serial_valid<=wide_valid;high_valid<=wide_valid;
  end else begin serial_word<=serial_high;serial_valid<=high_valid;end
  if(!enable)begin serial_valid<=0;high_valid<=0;end
 end
 assign word_data=serial_word;assign valid=serial_valid;

endmodule

// 80 bits at 100 MHz -> 32 bits at 250 MHz, no padding or dropped bytes.
// TRLC-LINKS: REQ-SDS-039
module interleave_gearbox(input clk,enable,input [79:0] q,input empty,input [4:0] used,input consume,
 output [31:0] word_data,output valid,pop,output reg fault=0);
 reg started=0;reg [2:0] slot=0;reg [79:0] saved=0;
 assign valid=started && !fault && ((slot==0 || slot==2) ? !empty : 1'b1);
 assign pop=consume && valid && (slot==0 || slot==2);
 assign word_data=slot==0 ? q[31:0] : slot==1 ? saved[63:32] : slot==2 ? {q[15:0],saved[79:64]} : slot==3 ? saved[47:16] : saved[79:48];
 always @(posedge clk)begin
  if(!enable)begin started<=0;slot<=0;fault<=0;end
  else begin
   if(!started && used>=8)started<=1;
   if(started && consume && !valid)fault<=1;
   if(consume && valid)begin
    if(pop)saved<=q;
    slot<=slot==4 ? 0 : slot+1'b1;
   end
  end
 end
endmodule

// Four 80-bit frames become five 64-bit transfers at 125 MHz.
// TRLC-LINKS: REQ-SDS-039
module interleave_gearbox64(input clk,enable,input [79:0] q,input empty,input [4:0] used,input consume,
 output [63:0] word_data,output valid,pop,output reg fault=0);
 reg started=0;reg [4:0] slot=5'b00001;reg [79:0] saved=0;
 assign valid=started && !fault && (slot[4] || !empty);
 assign pop=consume && valid && !slot[4];
 // One-hot selection avoids a priority chain on the FIFO-to-mailbox path.
 assign word_data=({64{slot[0]}} & q[63:0]) |
  ({64{slot[1]}} & {q[47:0],saved[79:64]}) |
  ({64{slot[2]}} & {q[31:0],saved[79:48]}) |
  ({64{slot[3]}} & {q[15:0],saved[79:32]}) |
  ({64{slot[4]}} & saved[79:16]);
 // Payload has no epoch state. Slot zero overwrites it before later slots
 // use it; keep the cross-domain enable off these 64 data registers.
 always @(posedge clk)if(pop)saved<=q;
 always @(posedge clk)begin
  if(!enable)begin started<=0;slot<=5'b00001;fault<=0;end
  else begin
   if(!started && used>=8)started<=1;
   if(started && consume && !valid)fault<=1;
   if(consume && valid)slot<={slot[3:0],slot[4]};
  end
 end
endmodule
