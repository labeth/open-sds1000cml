// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
// Raw ADC ordinals travel with data; extend their low word at the slower
// receiver clock. A frozen low word is held before the done synchronizer rises.
// LOW_WIDTH is reduced in simulation to exercise wraps without seconds of RTL.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-039, REQ-SDS-041
module sample_timeline #(parameter LOW_WIDTH=32)(
 input core,receiver_clk,core_enabled,receiver_enabled,raw_valid,
 output reg [LOW_WIDTH-1:0] raw_sample=0,
 input receiver_valid,input [LOW_WIDTH-1:0] receiver_sample,
 output reg [63:0] recent_sample=0,delayed_sample=0,
 input arm,write_step,raw_mode,input [LOW_WIDTH-1:0] write_sample,
 input frozen,input [31:0] record_id,record_epoch,
 output reg retained_valid=0,output reg [63:0] retained_last_word=0,
 output reg [31:0] retained_id=0,retained_epoch=0
);
 (* preserve, dont_merge *) reg count_enabled=0;
 always @(posedge core)count_enabled<=core_enabled;
 reg [LOW_WIDTH-1:0] last_receiver=0;
 reg [63-LOW_WIDTH:0] high=0;
 reg seen_receiver=0;
 // Adjacent valid receiver samples are separated by at most a handful of
 // ADC clocks, far less than half the low-word range. Its MSB falling is
 // therefore sufficient to detect a wrap without a wide comparator.
 wire wrapped=seen_receiver && last_receiver[LOW_WIDTH-1] && !receiver_sample[LOW_WIDTH-1];
 wire [63-LOW_WIDTH:0] extended_high=high+wrapped;
 wire [63:0] current_sample={extended_high,receiver_sample};
 reg [LOW_WIDTH-1:0] last_write=0,next_write=0;
 reg seen_write=0,contiguous=0;
 reg accepted_write=0,check_gap=0,candidate_step=0;
 reg [LOW_WIDTH-1:0] accepted_sample=0,expected_candidate=0;
 reg [LOW_WIDTH-1:0] candidate_source=0;
 localparam GROUPS=(LOW_WIDTH+7)/8;
 reg [GROUPS-1:0] gap_parts=0;
 reg [GROUPS-1:0] candidate_carry=0;
 reg [GROUPS-1:0] raw_carry=0;
 reg arm_toggle=0;
 always @(posedge core)begin
  candidate_step<=write_step && !arm;accepted_write<=candidate_step && !arm;
  candidate_source<=write_sample;accepted_sample<=candidate_source;
  if(accepted_write)begin last_write<=accepted_sample;next_write<=expected_candidate;end
  check_gap<=accepted_write && seen_write && !arm;
 end
 genvar g;generate for(g=0;g<GROUPS;g=g+1)begin:compare_gap
  localparam BITS=(LOW_WIDTH-8*g)<8 ? LOW_WIDTH-8*g : 8;
  if(g==0)always @(posedge core)begin
   if(!count_enabled)raw_sample[BITS-1:0]<=0;
   else if(raw_valid)raw_sample[BITS-1:0]<=raw_sample[BITS-1:0]+2'd2;
  end else begin
   always @(posedge core)begin
    if(!count_enabled)begin raw_sample[8*g+:BITS]<=0;raw_carry[g]<=0;end
    else if(raw_valid)begin
     raw_sample[8*g+:BITS]<=raw_sample[8*g+:BITS]+raw_carry[g];
     raw_carry[g]<=raw_sample[8*g-1:1]==({(8*g-1){1'b1}}-1'b1);
    end
   end
  end
  always @(posedge core)gap_parts[g]<=accepted_sample[8*g+:BITS]!=next_write[8*g+:BITS];
  // Register byte carries before addition; data and acceptance follow the
  // same two stages. Frozen metadata waits for this pipeline to settle.
  if(g==0)always @(posedge core)expected_candidate[BITS-1:0]<=candidate_source[BITS-1:0]+2'd2;
  else begin
   always @(posedge core)candidate_carry[g]<=&write_sample[8*g-1:1];
   always @(posedge core)expected_candidate[8*g+:BITS]<=candidate_source[8*g+:BITS]+candidate_carry[g];
  end
 end endgenerate
 always @(posedge core)begin
  if(arm)begin arm_toggle<=!arm_toggle;seen_write<=0;contiguous<=raw_mode && core_enabled;end
  else begin
   if(accepted_write)seen_write<=1;
   if(check_gap && (|gap_parts))contiguous<=0;
  end
 end
 (* async_reg="true" *) reg [2:0] frozen_sync=0;
 (* async_reg="true" *) reg [2:0] arm_sync=0;
 reg captured=0,seen_arm=0;
 reg [1:0] capture_stage=0;
 reg [63:0] live_snapshot=0;
 reg [LOW_WIDTH-1:0] last_snapshot=0;
 reg snapshot_valid=0,previous_wrap=0;
 reg [63-LOW_WIDTH:0] retained_high=0;
 always @(posedge receiver_clk)begin
  frozen_sync<={frozen_sync[1:0],frozen};
  arm_sync<={arm_sync[1:0],arm_toggle};
  if(!receiver_enabled)begin high<=0;last_receiver<=0;seen_receiver<=0;recent_sample<=0;delayed_sample<=0;end
  else if(receiver_valid)begin
   high<=extended_high;last_receiver<=receiver_sample;seen_receiver<=1;
   recent_sample<=current_sample;delayed_sample<=recent_sample;
  end
  // A one-word acquisition can be shorter than a receiver clock. The arm
  // toggle preserves its epoch even when the low frozen pulse is not sampled.
  if(arm_sync[2]!=seen_arm)begin seen_arm<=arm_sync[2];captured<=0;retained_valid<=0;capture_stage<=0;end
  else if(!frozen_sync[2])begin captured<=0;retained_valid<=0;capture_stage<=0;end
  else if(!captured)begin
   captured<=1;retained_id<=record_id;retained_epoch<=record_epoch;
   snapshot_valid<=receiver_enabled && seen_receiver && seen_write && contiguous;
   live_snapshot<=recent_sample;last_snapshot<=last_write;capture_stage<=1;
  end else if(capture_stage==1)begin
   // Snapshot comparison and upper-word correction have separate receiver
   // cycles; no cross-clock path contains a comparison plus a wide subtract.
   previous_wrap<=last_snapshot>live_snapshot[LOW_WIDTH-1:0];capture_stage<=2;
  end else if(capture_stage==2)begin
   retained_high<=live_snapshot[63:LOW_WIDTH]-previous_wrap;capture_stage<=3;
  end else if(capture_stage==3)begin
   retained_last_word<={retained_high,last_snapshot};retained_valid<=snapshot_valid;capture_stage<=0;
  end
 end
endmodule
