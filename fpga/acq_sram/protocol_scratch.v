// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
// USB cell history and Manchester candidates are mutually exclusive owners.
// Mode is held for a receiver epoch; reset blocks writes during handoff. Both
// clients see the same one-clock RAM latency as their private implementations.
// Two 256x33 banks preserve all 16 USB timestamps and both Manchester frame
// banks for both phase hypotheses, without clearing RAM on an epoch change.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018, REQ-SDS-039
module protocol_scratch(
 input wire clk,reset,manchester,
 input wire usb_write,input wire [3:0] usb_wr,usb_rd,
 input wire [63:0] usb_data,output wire [63:0] usb_q,
 input wire [1:0] man_write,input wire [7:0] man_wr_a,man_wr_b,man_rd,
 input wire [32:0] man_data_a,man_data_b,
 output wire [32:0] man_q_a,man_q_b
);
 (* ramstyle="M9K" *) reg [32:0] bank_a[0:255],bank_b[0:255];
 reg [32:0] qa=0,qb=0;
 wire [7:0] rd=manchester ? man_rd : {4'd0,usb_rd};
 wire [7:0] wr_a=manchester ? man_wr_a : {4'd0,usb_wr};
 wire [7:0] wr_b=manchester ? man_wr_b : {4'd0,usb_wr};
 wire write_a=!reset && (manchester ? man_write[0] : usb_write);
 wire write_b=!reset && (manchester ? man_write[1] : usb_write);
 wire [32:0] data_a=manchester ? man_data_a : {1'b0,usb_data[31:0]};
 wire [32:0] data_b=manchester ? man_data_b : {1'b0,usb_data[63:32]};
 always @(posedge clk)begin
  qa<=bank_a[rd];qb<=bank_b[rd];
  if(write_a)bank_a[wr_a]<=data_a;
  if(write_b)bank_b[wr_b]<=data_b;
 end
 assign usb_q={qb[31:0],qa[31:0]};
 assign man_q_a=qa;assign man_q_b=qb;
endmodule
