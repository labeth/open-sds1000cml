// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-HOST-RAM
// 5120 32-bit host words, packed as 2560 pairs in five explicit M9K sections.
// Logical bank boundary is pair 1280, not a physical RAM-section boundary.
// Caller owns collision avoidance and bank publication; concurrent read/write
// to the same pair is intentionally unspecified. No reset erases RAM contents.
// TRLC-LINKS: REQ-SDS-051
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
  // Keep the asymmetric read in the RAM primitive. A variable part-select
  // on an inferred 64-bit memory prevents RAM inference in Quartus 21.1.
`ifndef ALTERA_RESERVED_QIS
  reg [63:0] memory[0:511];
  reg [15:0] fetched=0;
  always @(posedge write_clk)
   if(!write_reset && write_enable && write_legal && write_pair[11:9]==section)
    memory[write_pair[8:0]]<=write_data;
  always @(posedge read_clk)fetched<=memory[read_halfword[10:2]][16*read_halfword[1:0]+:16];
  assign section_data[section]=fetched;
`else
  altsyncram #(
   .intended_device_family("Cyclone IV E"), .ram_block_type("M9K"),
   .operation_mode("DUAL_PORT"),
   .width_a(64), .widthad_a(9), .numwords_a(512), .width_byteena_a(1),
   .width_b(16), .widthad_b(11), .numwords_b(2048),
   .address_reg_b("CLOCK1"), .outdata_reg_b("UNREGISTERED"),
   .read_during_write_mode_mixed_ports("DONT_CARE"),
   .power_up_uninitialized("TRUE")
  ) memory(
   .clock0(write_clk), .clock1(read_clk),
   .clocken0(1'b1), .clocken1(1'b1), .clocken2(1'b1), .clocken3(1'b1),
   .aclr0(1'b0), .aclr1(1'b0),
   .address_a(write_pair[8:0]), .data_a(write_data),
   .wren_a(!write_reset && write_enable && write_legal && write_pair[11:9]==section),
   .byteena_a(1'b1), .rden_a(1'b1),
   .address_b(read_halfword[10:0]), .q_b(section_data[section]),
   .wren_b(1'b0), .data_b(16'b0), .byteena_b(1'b1), .rden_b(1'b1),
   .addressstall_a(1'b0), .addressstall_b(1'b0)
  );
`endif
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
