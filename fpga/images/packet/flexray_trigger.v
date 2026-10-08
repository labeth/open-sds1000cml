// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
// FlexRay frame trigger (ADR-PROTOCOL-PACKET-IMAGE), after DecodeFlexRay: a
// rising edge ending a LOW run of at least four bits after a falling edge is
// the FSS; each byte is BSS (HIGH, LOW) plus eight MSB-first bits sampled
// mid-cell, and the BSS falling edge within 0.4 bit of its nominal place
// re-anchors the byte. A failed BSS ends the frame. A frame is valid with at
// least the five header bytes, a matching header CRC-11 and, when it holds
// exactly 5+2*length+3 bytes, a matching frame CRC-24. START publishes with
// the first byte, DATA per byte, then END if valid or ERROR. Only END of a
// frame whose bytes hold the contiguous pattern matches.
// END/ERROR value: frame ID, payload length<<11, cycle<<18, sync<<24,
// startup<<25; count: bytes.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
// STAMP keeps only the low bits of sample ordinals; an image that shares one
// reconstruction of the upper bits sets it to 32 (events are milliseconds old).
module flexray_trigger #(parameter STAMP=64)(
 input wire clk,reset,enable,tick,line,inverted,
 input wire [23:0] bit_ticks_q8,
 input wire [63:0] pattern,input wire [3:0] pattern_len,
 input wire [63:0] sample,
 output reg match=0,event_valid=0,output reg [7:0] event_kind=0,
 output reg [31:0] event_data=0,output reg [15:0] event_count=0,
 output wire [63:0] event_sample
);
 reg [STAMP-1:0] stamp=0;assign event_sample=stamp;
 wire [STAMP-1:0] now=sample[STAMP-1:0];
 localparam FSS=4'd15;
 reg valid_config=0,active=0,previous=1,primed=0,fell=0,resynced=0,started=0,hit=0,check=0;
 reg [23:0] low_ticks=0,tss_ticks=0,early=0,late=0;
 // Countdowns to the current cell's end and middle, in Q8 receiver clocks.
 // Reloads keep the fraction, which whole-clock countdown leaves unchanged,
 // so each is prepared a clock ahead. The resync window is registered from
 // the previous clock's countdown.
 reg [23:0] left=0,mid=0,end_reload=0,mid_reload=0,anchor_left=0,anchor_mid=0,window_end=0,window_start=0;
 reg mid_taken=0,in_window=0;
 reg [7:0] lanes=0;
 reg [3:0] bit_number=FSS;
 reg [8:0] bytes=0;
 reg [7:0] byte_shift=0;
 reg [5:0] header_bit=0;
 reg [39:0] header=0;
 reg [10:0] header_crc=0;
 reg [23:0] frame_crc=0,trailer=0;
 reg [63:0] window=0;reg [STAMP-1:0] frame_sample=0,byte_sample=0;
 wire level=line^inverted;
 wire rising=primed && !previous && level;
 wire falling=primed && previous && !level;
 wire middle=!mid_taken && mid<24'd256;
 wire bit_end=left<24'd256;
 wire [7:0] byte_next={byte_shift[6:0],level};
 wire [63:0] window_next={window[55:0],byte_next};

 // Byte lanes enabled by the pattern length replace a 64-bit mask.
 wire [7:0] lane_equal;
 genvar lane_i;
 generate for(lane_i=0;lane_i<8;lane_i=lane_i+1)begin:lane_compare
  assign lane_equal[lane_i]=window[8*lane_i+:8]==pattern[8*lane_i+:8];
 end endgenerate
 wire window_hit=pattern_len==0 || ({1'b0,bytes}>=pattern_len && &(lane_equal|~lanes));
 wire hit_now=hit || (check && window_hit);
 wire [6:0] payload=header[23:17];
 wire [9:0] crc_bytes=10'd5+{2'd0,payload,1'b0};
 wire [9:0] total_bytes=crc_bytes+10'd3;
 wire [10:0] header_crc_next={header_crc[9:0],1'b0}^(header_crc[10]^level ? 11'h385 : 11'd0);
 wire [23:0] frame_crc_next={frame_crc[22:0],1'b0}^(frame_crc[23]^level ? 24'h5d6dcb : 24'd0);
 wire header_good=bytes>=5 && header_crc==header[16:6];
 wire frame_good=header_good && ({1'b0,bytes}!=total_bytes || frame_crc==trailer);
 wire [31:0] identity={6'd0,header[35],header[36],header[5:0],header[23:17],header[34:24]};
 always @(posedge clk)begin
  valid_config<=bit_ticks_q8>=24'd1024 && pattern_len<=8;
  tss_ticks<={bit_ticks_q8[23:8],2'd0};
  early<=(bit_ticks_q8*8'd154)>>8;
  late<=(bit_ticks_q8*8'd102)>>8;
  end_reload<={16'd0,left[7:0]}+bit_ticks_q8-24'd256;
  mid_reload<={16'd0,left[7:0]}+(bit_ticks_q8>>1)-24'd256;
  // An anchor places this clock half a receiver clock into bit 1.
  anchor_left<=bit_ticks_q8-24'd128;anchor_mid<=(bit_ticks_q8>>1)-24'd128;
  window_end<=bit_ticks_q8-early;window_start<=bit_ticks_q8-late;
  in_window<=(bit_number==0 && left<=window_end) || (bit_number==1 && left>=window_start);
  lanes<=pattern_len>=8 ? 8'hff : (8'd1<<pattern_len)-1'b1;
 end
 always @(posedge clk)begin
  event_valid<=0;match<=0;check<=0;hit<=hit_now;
  if(reset || !enable || !valid_config)begin
   active<=0;primed<=0;previous<=1;fell<=0;low_ticks<=0;
  end else if(tick)begin
   primed<=1;previous<=level;
   if(level)low_ticks<=0;
   else if(falling)begin low_ticks<=1;fell<=1;end
   else if(low_ticks!=24'hffffff)low_ticks<=low_ticks+1'b1;
   if(!active)begin
    if(rising && fell && low_ticks>=tss_ticks)begin
     active<=1;bit_number<=FSS;left<=anchor_left;mid<=anchor_mid;mid_taken<=0;resynced<=0;started<=0;hit<=0;
     bytes<=0;header_bit<=0;header_crc<=11'h1a;frame_crc<=24'hfedcba;window<=0;
     frame_sample<=now;
    end
   end else begin
    left<=bit_end ? end_reload : left-24'd256;
    mid<=bit_end ? mid_reload : mid-24'd256;
    if(middle)mid_taken<=1;
    if(bit_end)begin
     mid_taken<=0;
     bit_number<=bit_number==9 ? 4'd0 : bit_number+1'b1;
     if(bit_number==9 || bit_number==FSS)begin resynced<=0;byte_sample<=now;end
    end
    // The BSS falling edge re-anchors the byte (DecodeFlexRay resync).
    if(falling && !resynced && in_window)begin
     bit_number<=1;left<=anchor_left;mid<=anchor_mid;mid_taken<=0;resynced<=1;
    end else if(middle)begin
     case(bit_number)
      FSS:;
      0:if(!level)active<=0;
      1:if(level)active<=0;
       else if(!started)begin
        started<=1;event_valid<=1;event_kind<=1;event_data<=0;event_count<=0;stamp<=frame_sample;
       end
      default:begin
       byte_shift<=byte_next;
       if(bytes<5)begin
        header<={header[38:0],level};header_bit<=header_bit+1'b1;
        if(header_bit>=3 && header_bit<=22)header_crc<=header_crc_next;
       end
       if({1'b0,bytes}<crc_bytes || bytes<5)frame_crc<=frame_crc_next;
       else trailer<={trailer[22:0],level};
       if(bit_number==9)begin
        window<=window_next;check<=1;
        event_valid<=1;event_kind<=2;event_data<={24'd0,byte_next};event_count<={7'd0,bytes};
        stamp<=byte_sample;
        if(bytes==9'h1ff)active<=0;else bytes<=bytes+1'b1;
       end
      end
     endcase
    end
    // A failed BSS closes the frame; a frame without bytes publishes nothing.
    if(middle && !(falling && !resynced && in_window) && ((bit_number==0 && !level) || (bit_number==1 && level)) && started)begin
     event_valid<=1;stamp<=now;event_data<=identity;event_count<={7'd0,bytes};
     if(frame_good && bytes!=9'h1ff)begin event_kind<=3;match<=hit_now;end
     else event_kind<=4;
    end
   end
  end
 end
endmodule
