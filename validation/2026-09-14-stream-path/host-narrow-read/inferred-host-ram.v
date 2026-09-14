// 5120 32-bit host words, packed as 2560 pairs in five explicit M9K sections.
// Logical bank boundary is pair 1280, not a physical RAM-section boundary.
// Caller owns collision avoidance and bank publication; concurrent read/write
// to the same pair is intentionally unspecified. No reset erases RAM contents.
module sram_host_ram(
 input wire write_clk,write_reset,write_enable,
 input wire [11:0] write_pair,input wire [63:0] write_data,
 output reg write_fault=0,
 input wire read_clk,read_reset,read_enable,
 input wire [13:0] read_halfword,
 output reg read_valid=0,read_error=0,output reg [15:0] read_data=0
);
 wire write_legal=write_pair<2560;
 always @(posedge write_clk)begin
  if(write_reset)write_fault<=0;
  else if(write_enable && !write_legal)write_fault<=1;
 end
 wire [15:0] section_data[0:4];
 genvar section;
 generate for(section=0;section<5;section=section+1)begin: sections
  (* ramstyle = "M9K, no_rw_check" *) reg [63:0] memory[0:511];
  reg [15:0] fetched=0;
  always @(posedge write_clk)
   if(!write_reset && write_enable && write_legal && write_pair[11:9]==section)
    memory[write_pair[8:0]]<=write_data;
  always @(posedge read_clk)fetched<=memory[read_halfword[10:2]][16*read_halfword[1:0]+:16];
  assign section_data[section]=fetched;
 end endgenerate
 reg pending=0,illegal=0;
 reg [2:0] selected=0;
 reg [15:0] selected_data;
 integer n;
 always @* begin
  selected_data=0;
  for(n=0;n<5;n=n+1)if(selected==n)selected_data=section_data[n];
 end
 // Request captured at edge N; response appears after edge N+1. The interface
 // accepts one request per read clock, including explicitly rejected addresses.
 always @(posedge read_clk)begin
  selected<=read_halfword[13:11];
  if(read_reset)begin pending<=0;illegal<=0;read_valid<=0;read_error<=0;read_data<=0;end
  else begin
   pending<=read_enable;illegal<=read_halfword>=10240;
   read_valid<=pending;read_error<=pending && illegal;
   read_data<=illegal ? 16'b0 : selected_data;
  end
 end
endmodule
