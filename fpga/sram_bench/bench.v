`timescale 1ns/1ps
module sram_bench_top(
 input clk, mclk_in, nCS1, nOE, nWE, input [6:2] sel,
 input gpmc_a2,gpmc_b1, inout [15:0] gpmc_d, inout [31:0] dq,
 output k1,k2,g1,g2,d1,d2,f1,f2,j2,a11,
 output [4:0] enc_p,enc_n
);
 wire test_clk, sample_clk, locked;
 bench_pll pll(mclk_in,test_clk,sample_clk,locked);
 assign g2=0; assign d1=0; assign d2=0; assign f1=1;
 assign f2=1; assign j2=1; assign a11=1;
 assign enc_p=0; assign enc_n=5'b11111;
 wire wc,rp,drive; wire [7:0] ws,rs; wire [15:0] wd; wire [1:0] aux;
 reg [15:0] rd;
 gpmc_slave busif(clk,nCS1,nOE,nWE,{sel,gpmc_a2,gpmc_b1},gpmc_d,wc,ws,wd,aux,rp,rs,rd,drive);
 reg req=0; reg snap_req=0; reg [31:0] seed=32'h13579bdf;
 reg [2:0] latency=3; reg [4:0] head_index=0;
 always @(posedge clk) if(wc) case(ws)
  1: req<=~req;
  2: seed[15:0]<=wd;
  3: seed[31:16]<=wd;
  4: latency<=wd[2:0];
  5: head_index<=wd[4:0];
  22: snap_req<=~snap_req;
 endcase
 reg [2:0] req_s=0; reg seen=0;
 reg [31:0] free_count=0, snap_count=0;reg free_carry=0;
 reg [2:0] snap_sync=0; reg snap_ack=0;
 always @(posedge test_clk) begin
  free_carry<=(free_count[15:0]==16'hfffe);
  free_count[15:0]<=free_count[15:0]+1'b1;
  if(free_carry) free_count[31:16]<=free_count[31:16]+1'b1;snap_sync<={snap_sync[1:0],snap_req};
  if(snap_sync[2]!=snap_ack) begin snap_count<=free_count;snap_ack<=snap_sync[2];end
 end
 reg [10:0] state=11'b1;
 reg clear_results=0;
 always @(posedge test_clk) clear_results<=(state[INIT]);
 localparam IDLE=0,TURN_W=1,WRITE=2,TURN_R=3,READ=4,DONE=5,DRAIN=6,ARM_W=7,INIT=8,WARM_WRITE=9,HOLD_W=10;
 wire start_run=locked && (req_s[2]!=seen) && (state[IDLE] || state[DONE]);
 always @(posedge test_clk) begin
  state[IDLE]<=state[IDLE] && !start_run;
  state[INIT]<=start_run;
  state[TURN_W]<=state[INIT] || (state[TURN_W] && !wait7);
  state[ARM_W]<=(state[TURN_W] && wait7) || (state[ARM_W] && !wait7);
  state[WARM_WRITE]<=(state[ARM_W] && wait7) || (state[WARM_WRITE] && !wait15);
  state[WRITE]<=(state[WARM_WRITE] && wait15) || (state[WRITE] && !end_write);
  state[HOLD_W]<=(state[WRITE] && end_write) || (state[HOLD_W] && !wait7);
  state[TURN_R]<=(state[HOLD_W] && wait7) || (state[TURN_R] && !wait31);
  state[READ]<=(state[TURN_R] && wait31) || (state[READ] && !end_read);
  state[DRAIN]<=(state[READ] && end_read) || (state[DRAIN] && !wait3);
  state[DONE]<=(state[DRAIN] && wait3) || (state[DONE] && !start_run);
 end
 reg [3:0] state_code;
 always @* begin
  state_code=15;
  case(1'b1) // synthesis parallel_case
   state[IDLE]:state_code=0;state[TURN_W]:state_code=1;state[WRITE]:state_code=2;
   state[TURN_R]:state_code=3;state[READ]:state_code=4;state[DONE]:state_code=5;
   state[DRAIN]:state_code=6;state[ARM_W]:state_code=7;state[INIT]:state_code=8;state[WARM_WRITE]:state_code=9;state[HOLD_W]:state_code=10;
  endcase
 end
 reg [19:0] pos=0;
 always @(posedge test_clk) if(state[WRITE] || state[READ]) pos<=pos+1'b1;else pos<=0;
 reg pos_hi_write=0,pos_hi_read=0;
 always @(posedge test_clk) begin
  pos_hi_write<=(pos[19:8]==12'h7ff);pos_hi_read<=(pos[19:8]==12'h800);
 end reg [4:0] wait_cycles=0;
 always @(posedge test_clk) begin
  if(((state[TURN_W] || state[ARM_W] || state[HOLD_W]) && !wait7) ||
     (state[WARM_WRITE] && !wait15) || (state[TURN_R] && !wait31) || (state[DRAIN] && !wait3))
   wait_cycles<=wait_cycles+1'b1;
  else wait_cycles<=0;
 end
 reg end_write=0,end_read=0,head_valid=0;reg wait7=0,wait31=0,wait3=0,wait15=0;
 always @(posedge test_clk) begin wait7<=(wait_cycles==6);wait31<=(wait_cycles==30);wait3<=(wait_cycles==2);wait15<=(wait_cycles==14);end
 always @(posedge test_clk) begin end_write<=(pos_hi_write && pos[7:0]==8'hfe);end_read<=(pos_hi_read && pos[7:0]==8'h1e);end
 reg [31:0] pattern=0, expected=0, pattern_out=0;
 reg pattern_run=0,stop_pattern=0;
 always @(posedge test_clk) begin
  stop_pattern<=pos_hi_write && pos[7:0]==8'hfc;
  if(state[INIT]) pattern_run<=0;
  if(state[WARM_WRITE] && wait_cycles==14) pattern_run<=1;
  if(state[WRITE] && stop_pattern) pattern_run<=0;
  if(clear_results) pattern<=seed;else if(pattern_run) pattern<=advance(pattern);
 end
 always @(posedge test_clk) pattern_out<=pattern;
 reg [19:0] error_count=0,checked_count=0;
 wire [31:0] errors={12'b0,error_count},checked={12'b0,checked_count};
 reg [31:0] first_index=0, first_want=0,first_got=0;
 reg cmp_valid=0,had_error=0; reg [3:0] cmp_bad_lane=0; wire cmp_bad=|cmp_bad_lane;
 reg [19:0] cmp_index=0;
 reg [31:0] cmp_want=0,cmp_got=0,bad_mask=0;
 reg save_first=0;reg [31:0] save_want=0,save_got=0;reg [19:0] save_index=0;
 reg [31:0] read_head[0:31];
 reg [31:0] captured=0,core_sample=0;
 always @(posedge test_clk) core_sample<=captured;
 reg [20:0] cycle_count=0;wire [31:0] cycles={11'b0,cycle_count};
 reg [2:0] delay_latched=0; reg check_enable=0;reg [5:0] check_start=0;reg wrap_expected=0;
 always @(posedge test_clk) wrap_expected<=(checked_count==20'd524270);
 function [31:0] advance(input [31:0] x);
  begin advance={x[30:0],x[31]^x[21]^x[1]^x[0]};end
 endfunction
 assign g1=(state[ARM_W] || state[WARM_WRITE] || state[WRITE] || state[HOLD_W] || state[TURN_R] || state[READ]);
 assign k1=!(state[TURN_W] || state[ARM_W] || state[WARM_WRITE] || state[WRITE] || state[HOLD_W]);
 assign dq=(state[ARM_W] || state[WARM_WRITE] || state[WRITE] || state[HOLD_W]) ? pattern_out : 32'bz;
 reg clock_on=0,stop_write_early=0,stop_read_early=0;
 always @(posedge test_clk) begin
  stop_write_early<=(pos_hi_write && pos[7:0]==8'hfd);
  stop_read_early<=(pos_hi_read && pos[7:0]==8'h1d);
  if(state[INIT]) clock_on<=1;
  if(state[TURN_W] && wait_cycles==6) clock_on<=0;
  if(state[ARM_W] && wait_cycles==6) clock_on<=1;
  if(state[WRITE] && stop_write_early) clock_on<=0;
  if(state[TURN_R] && wait_cycles==30) clock_on<=1;
  if(state[READ] && stop_read_early) clock_on<=0;
 end
 // Launch data/control at rising core edge; K2 rises half a cycle later.
 altddio_out #(.width(1),.power_up_high("OFF"),.intended_device_family("Cyclone IV E")) ckout
 (.outclock(test_clk),.datain_h(1'b0),.datain_l(clock_on),.dataout(k2),
 .oe(1'b1),.aclr(!locked),.aset(1'b0),.sclr(1'b0),.sset(1'b0),.outclocken(1'b1));
 always @(posedge sample_clk) captured<=dq;
 always @(posedge test_clk) begin
  req_s<={req_s[1:0],req};
  cmp_valid<=0;
  save_first<=cmp_valid && cmp_bad && !had_error;
  save_want<=cmp_want;save_got<=cmp_got;save_index<=cmp_index;
  if(save_first) begin first_index<={12'b0,save_index};first_want<=save_want;first_got<=save_got;end
  if(cmp_valid) bad_mask<=bad_mask|(cmp_want^cmp_got);
  if(cmp_valid && cmp_bad) begin
   error_count<=error_count+1'b1;had_error<=1;

  end
  if(!state[IDLE] && !state[DONE]) cycle_count<=cycle_count+1;
  case(1'b1) // synthesis parallel_case
   state[IDLE],state[DONE]: if(locked && req_s[2]!=seen) begin
    seen<=req_s[2];
   end
   state[INIT]: begin
    check_start<=6'd15+latency;
    delay_latched<=latency;
    cycle_count<=0;
   end
   state[TURN_W]: if(wait7) begin end
           
   state[ARM_W]: if(wait7) begin end
           
   state[WARM_WRITE]: begin
    expected<=advance(expected);
    if(wait15) begin end
    
   end
   state[WRITE]: begin
    if(end_write) begin end
    
   end

   state[TURN_R]: begin
    if(wait31) begin head_valid<=1;check_enable<=0;end
    
   end
   state[READ]: begin
    if(`BENCH_MHZ<=200 && head_valid) read_head[pos[4:0]]<=core_sample;
    if(pos[4:0]==31) head_valid<=0;
    if(!check_enable && pos[5:0]==check_start) check_enable<=1;
    if(check_enable && !checked_count[19]) begin
     checked_count<=checked_count+1'b1;
     cmp_valid<=1;
     cmp_bad_lane<={core_sample[31:24]!=expected[31:24],core_sample[23:16]!=expected[23:16],core_sample[15:8]!=expected[15:8],core_sample[7:0]!=expected[7:0]};
     cmp_index<=checked_count;cmp_want<=expected;cmp_got<=core_sample;
     if(wrap_expected) expected<=seed;else expected<=advance(expected);
    end
    if(end_read) begin end 
   end

  endcase
  if(clear_results) begin
   expected<=seed;
   error_count<=0;checked_count<=0;first_index<=32'hffffffff;had_error<=0;bad_mask<=0;
  end
 end
 // Results are read after DONE and are stable across the bus clock domain.
 always @* begin
  rd=0;
  case(rs)
   0:rd=16'h5b51;
   1:rd={11'b0,locked,state_code};
   2:rd=seed[15:0];3:rd=seed[31:16];4:rd={13'b0,latency};
   6:rd=errors[15:0];7:rd=errors[31:16];
   8:rd=checked[15:0];9:rd=checked[31:16];
   10:rd=first_index[15:0];11:rd=first_index[31:16];
   12:rd=first_want[15:0];13:rd=first_want[31:16];
   14:rd=first_got[15:0];15:rd=first_got[31:16];
   16:rd=(`BENCH_MHZ<=200)?read_head[head_index][15:0]:0;17:rd=(`BENCH_MHZ<=200)?read_head[head_index][31:16]:0;
   18:rd=cycles[15:0];19:rd=cycles[31:16];
   20:rd=`BENCH_MHZ;21:rd=16'h0018;
   23:rd=snap_count[15:0];24:rd=snap_count[31:16];25:rd={15'b0,snap_ack};
   28:rd=bad_mask[15:0];29:rd=bad_mask[31:16];
  endcase
 end
endmodule
