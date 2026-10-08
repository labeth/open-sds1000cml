// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
// CAN / CAN FD frame trigger (ADR-PROTOCOL-PACKET-IMAGE), after DecodeCANFD:
// open-loop cells from the SOF edge sampled mid-cell, dynamic destuffing, the
// FD data rate after a recessive BRS and classic CRC-15 over SOF..data. FD
// frames end after the data field like the reference. A SOF counts only after
// seven recessive nominal bits. START and DATA publish while the frame
// arrives; END follows a valid frame, ERROR a stuff violation or CRC mismatch.
// Only END of a frame whose data field holds the contiguous pattern matches.
// END/ERROR value: identifier[28:0], IDE, FD, RTR; count: DLC<<8 | bytes.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
// STAMP keeps only the low bits of sample ordinals; an image that shares one
// reconstruction of the upper bits sets it to 32 (events are milliseconds old).
module can_trigger #(parameter STAMP=64)(
 input wire clk,reset,enable,tick,line,inverted,
 input wire [23:0] bit_ticks_q8,data_ticks_q8,
 input wire [63:0] pattern,input wire [3:0] pattern_len,
 input wire [63:0] sample,
 output reg match=0,event_valid=0,output reg [7:0] event_kind=0,
 output reg [31:0] event_data=0,output reg [15:0] event_count=0,
 output wire [63:0] event_sample
);
 reg [STAMP-1:0] stamp=0;assign event_sample=stamp;
 wire [STAMP-1:0] now=sample[STAMP-1:0];
 localparam P_SOF=0,P_ID=1,P_B1=2,P_IDE=3,P_FDF=4,P_RES=5,P_BRS=6,P_ESI=7,
  P_EXT=8,P_RTR=9,P_R1=10,P_R0=11,P_DLC=12,P_DATA=13,P_CRC=14;
 reg active=0,valid_config=0,data_rate=0;
 reg [3:0] field=P_SOF;
 reg [4:0] count=0;
 reg [23:0] remaining=0,cell_reload=0,switch_reload=0,half_reload=0;
 reg [19:0] quiet=0,idle_ticks=0;
 reg [2:0] run_len=0;reg run_val=0,stuff_error=0;
 reg [28:0] id=0;reg b1=0,ide=0,fd=0,remote=0;
 reg [3:0] dlc=0;reg [6:0] bytes=0,byte_total=0;
 reg [7:0] byte_shift=0;reg [2:0] bit_index=0;
 reg [14:0] crc=0,rx_crc=0;
 reg [63:0] window=0;reg [STAMP-1:0] byte_sample=0;
 reg hit=0,closing=0,check=0,rates_differ=0;
 reg [23:0] half_sum=0;
 reg [7:0] lanes=0;
 wire level=line^inverted; // 0 dominant, 1 recessive
 wire sampling=active && tick && remaining<256;
 wire stuff=run_len==5;
 wire bit_valid=sampling && !stuff;
 wire [3:0] dlc_next={dlc[2:0],level};
 wire [7:0] byte_next={byte_shift[6:0],level};
 wire [63:0] window_next={window[55:0],byte_next};
 wire [14:0] crc_next={crc[13:0],1'b0}^(crc[14]^level ? 15'h4599 : 15'd0);
 // The window is compared the clock after each byte lands, against a mask
 // prepared from the held configuration.

 // Byte lanes enabled by the pattern length replace a 64-bit mask.
 wire [7:0] lane_equal;
 genvar lane_i;
 generate for(lane_i=0;lane_i<8;lane_i=lane_i+1)begin:lane_compare
  assign lane_equal[lane_i]=window[8*lane_i+:8]==pattern[8*lane_i+:8];
 end endgenerate
 wire window_hit=pattern_len==0 || ({1'b0,bytes}>=pattern_len && &(lane_equal|~lanes));
 wire hit_now=hit || (check && window_hit);
 function [6:0] fd_length(input [3:0] code);
  case(code)
   9:fd_length=12;10:fd_length=16;11:fd_length=20;12:fd_length=24;
   13:fd_length=32;14:fd_length=48;15:fd_length=64;default:fd_length={3'd0,code};
  endcase
 endfunction
 wire [6:0] total_next=(!fd && remote) ? 7'd0 : fd ? fd_length(dlc_next) : (dlc_next>8 ? 7'd8 : {3'd0,dlc_next});
 wire [31:0] identity={remote,fd,ide,id};
 always @(posedge clk)begin
  valid_config<=bit_ticks_q8>=24'd2048 && data_ticks_q8>=24'd2048 && pattern_len<=8;
  idle_ticks<=({4'd0,bit_ticks_q8[23:8]}<<3)-{4'd0,bit_ticks_q8[23:8]};
  // The fraction is invariant under whole-tick countdown, so each reload
  // can be prepared a clock before the sample that consumes it.
  cell_reload<={16'd0,remaining[7:0]}+(data_rate ? data_ticks_q8 : bit_ticks_q8)-24'd256;
  half_sum<=(bit_ticks_q8>>1)+(data_ticks_q8>>1)-24'd256;
  switch_reload<={16'd0,remaining[7:0]}+half_sum;
  rates_differ<=data_ticks_q8!=bit_ticks_q8;
  half_reload<=(bit_ticks_q8>>1)-24'd256;
  lanes<=pattern_len>=8 ? 8'hff : (8'd1<<pattern_len)-1'b1;
 end
 always @(posedge clk)begin
  event_valid<=0;match<=0;closing<=0;check<=0;hit<=hit_now;
  // An FD frame ends on its last data byte, whose DATA event holds this
  // clock's slot; END or ERROR follows on the next clock.
  if(closing)begin
   event_valid<=1;stamp<=now;event_data<=identity;event_count<={4'd0,dlc,1'b0,bytes};
   if(stuff_error)event_kind<=4;else begin event_kind<=3;match<=hit_now;end
  end
  if(reset || !enable || !valid_config)begin
   active<=0;quiet<=0;field<=P_SOF;data_rate<=0;
  end else if(tick)begin
   if(!active)begin
    if(level)begin if(quiet!=20'hfffff)quiet<=quiet+1'b1;end
    else begin
     if(quiet>=idle_ticks)begin
      active<=1;remaining<=half_reload;field<=P_SOF;count<=0;data_rate<=0;
      run_len<=0;run_val<=1;stuff_error<=0;crc<=0;hit<=0;bytes<=0;window<=0;
      id<=0;fd<=0;remote<=0;ide<=0;dlc<=0;bit_index<=0;byte_sample<=now;
     end
     quiet<=0;
    end
   end else if(!sampling)remaining<=remaining-24'd256;
   else begin
    remaining<=cell_reload;
    // Stuff bits restart the run; a stuff bit equal to its run is a violation.
    if(stuff)begin
     run_len<=1;run_val<=level;
     if(level==run_val)stuff_error<=1;
    end else begin
     if(level==run_val)run_len<=run_len+1'b1;
     else begin run_val<=level;run_len<=1;end
    end
    if(bit_valid)begin
     if(field!=P_CRC)crc<=crc_next;
     count<=count+1'b1;
     case(field)
      P_SOF:if(level)active<=0;
       else begin
        field<=P_ID;count<=0;
        event_valid<=1;event_kind<=1;event_data<=0;event_count<=0;stamp<=byte_sample;
       end
      P_ID:begin id<={id[27:0],level};if(count==10)field<=P_B1;end
      P_B1:begin b1<=level;field<=P_IDE;end
      P_IDE:begin ide<=level;count<=0;field<=level ? P_EXT : P_FDF;end
      P_FDF:begin
       count<=0;
       if(level)begin fd<=1;field<=P_RES;end
       else begin remote<=b1;field<=P_DLC;end
      end
      P_RES:field<=P_BRS;
      P_BRS:begin
       field<=P_ESI;
       if(level && rates_differ)begin data_rate<=1;remaining<=switch_reload;end
      end
      P_ESI:begin field<=P_DLC;count<=0;end
      P_EXT:begin id<={id[27:0],level};if(count==17)field<=P_RTR;end
      P_RTR:begin remote<=level;field<=P_R1;end
      P_R1:field<=P_R0;
      P_R0:begin field<=P_DLC;count<=0;end
      P_DLC:begin
       dlc<=dlc_next;
       if(count==3)begin
        byte_total<=total_next;count<=0;bit_index<=0;
        if(total_next!=0)field<=P_DATA;
        else if(!fd)field<=P_CRC;
        else begin
         active<=0;quiet<=0;event_valid<=1;stamp<=now;
         event_kind<=stuff_error ? 8'd4 : 8'd3;event_data<={remote,1'b1,ide,id};event_count<={4'd0,dlc_next,8'd0};
        end
       end
      end
      P_DATA:begin
       byte_shift<=byte_next;bit_index<=bit_index+1'b1;
       if(bit_index==0)byte_sample<=now;
       if(bit_index==7)begin
        window<=window_next;bytes<=bytes+1'b1;check<=1;
        event_valid<=1;event_kind<=2;event_data<={24'd0,byte_next};event_count<={9'd0,bytes};
        stamp<=byte_sample;
        if(bytes+1'b1==byte_total)begin
         count<=0;
         if(!fd)field<=P_CRC;
         else begin
          field<=P_SOF;active<=0;quiet<=0;closing<=1;
         end
        end
       end
      end
      P_CRC:begin
       rx_crc<={rx_crc[13:0],level};
       if(count==14)begin
        active<=0;quiet<=0;event_valid<=1;stamp<=now;
        event_data<=identity;event_count<={4'd0,dlc,1'b0,bytes};
        if(stuff_error || {rx_crc[13:0],level}!=crc)event_kind<=4;
        else begin event_kind<=3;match<=hit_now;end
       end
      end
     endcase
    end
   end
  end
 end
endmodule
