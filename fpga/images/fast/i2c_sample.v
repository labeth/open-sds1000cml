// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
`timescale 1ns/1ps
// I2C receiver on the sample-domain edges of two sample_edges.v extractors:
// SCL's rising crossings, each with SDA's level at its interpolated time
// (the bit), and SDA's crossings in both directions, each with SCL's level
// at its own time: SCL high, a falling one is a START (repeated or not) and
// a rising one a STOP. Crossings of the two lines in one receiver clock are
// taken in the order of their times (SCL's on SDA's grid: its time plus
// delay), the later on the next clock: one is held, a third in that clock is
// dropped; the ordering takes a clock of its own. Events, metadata, the
// selection and the match are i2c_trigger.v's.
// The app's Go model (internal/decode sample_bus.go SampleI2C) is this
// receiver clock for clock. Configuration is held while enabled.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-229
module i2c_sample(
 input wire clk,reset,enable,
 input wire live,
 input wire scl_found,scl_other,       // SCL's rising crossing, SDA at it
 input wire signed [7:0] scl_when,
 input wire sda_found,sda_rising,sda_other, // SDA's crossing, SCL at it
 input wire signed [7:0] sda_when,
 input wire signed [7:0] delay,        // SCL's time on SDA's grid: its own plus delay
 input wire inverted,
 input wire [6:0] address,
 input wire any_address,
 input wire [1:0] direction,           // 0 write, 1 read, 2 either
 input wire [31:0] pattern,
 input wire [2:0] pattern_len,
 output reg match=0,
 output reg event_valid=0,
 output reg [7:0] event_kind=0,event_data=0,
 output reg [1:0] event_meta=0,        // DATA: bit0 address byte, bit1 NACK
 output reg selected=0
);
 // The clock's crossings, {valid, SDA's, rising, other line}, put in time
 // order a clock later (timing); the one held from the clock before.
 reg [3:0] c_scl=0,c_sda=0;reg sda_first=0;
 always @(posedge clk)begin
  c_scl<={scl_found && live,1'b0,1'b1,scl_other^inverted};
  c_sda<={sda_found && live,1'b1,sda_rising^inverted,sda_other^inverted};
  sda_first<=scl_found && sda_found && $signed({sda_when[7],sda_when})<$signed({scl_when[7],scl_when})+$signed({delay[7],delay});
 end
 wire [3:0] first=(sda_first || !c_scl[3]) ? c_sda : c_scl;
 wire [3:0] second=sda_first ? c_scl : c_scl[3] ? c_sda : 4'd0;
 reg [3:0] held=0;
 wire [3:0] now=held[3] ? held : first;
 wire valid=now[3],is_sda=now[2],up=now[1],line=now[0];
 reg active=0,address_byte=1,received_address=0;
 reg [3:0] bit_index=0;
 reg [7:0] shift=0;
 reg [31:0] history=0;
 reg [2:0] count=0;
 wire start=valid && is_sda && line && !up;
 wire stop=valid && is_sda && line && up;
 wire rise=valid && !is_sda;
 wire [7:0] received={shift[6:0],line};
 wire [31:0] next_history={history[23:0],received};
 wire address_match=(any_address || received[7:1]==address) &&
  (direction==2 || (direction==0 && !received[0]) || (direction==1 && received[0]));
 wire data_match=(pattern_len==1 && next_history[7:0]==pattern[7:0]) ||
  (pattern_len==2 && count>=1 && next_history[15:0]==pattern[15:0]) ||
  (pattern_len==3 && count>=2 && next_history[23:0]==pattern[23:0]) ||
  (pattern_len==4 && count>=3 && next_history==pattern);
 always @(posedge clk)begin
  match<=0;event_valid<=0;
  if(reset || !enable || pattern_len>4 || direction>2 || !live)begin
   held<=0;active<=0;selected<=0;count<=0;history<=0;bit_index<=0;address_byte<=1;
  end else begin
   held<=held[3] ? first : second;
   if(start)begin
    event_valid<=1;event_kind<=1;event_data<=0;event_meta<=0;
    active<=1;selected<=0;count<=0;history<=0;bit_index<=0;address_byte<=1;
   end else if(stop)begin
    if(active)begin event_valid<=1;event_kind<=3;event_data<=0;event_meta<=0;end
    active<=0;selected<=0;count<=0;history<=0;
   end else if(active && rise)begin
    if(bit_index==8)begin
     bit_index<=0;event_valid<=1;event_kind<=2;event_data<=shift;
     event_meta<={line,received_address};
    end else begin
     shift<=received;bit_index<=bit_index+1'b1;
     if(bit_index==7)begin
      received_address<=address_byte;
      if(address_byte)begin
       address_byte<=0;selected<=address_match;
       match<=address_match && pattern_len==0;
      end else if(selected)begin
       history<=next_history;if(count<4)count<=count+1'b1;
       match<=data_match;
      end
     end
    end
   end
  end
 end
endmodule
