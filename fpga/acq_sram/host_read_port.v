// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-HOST-READ-PORT
// Profile host readout register window. All ports use the GPMC host clock.
// Descriptors come from sram_host_ownership's coherent host-domain outputs.
// TRLC-LINKS: REQ-SDS-053
module acq_host_read_port(
 input wire clk,reset,host_write,host_pop,
 input wire [7:0] write_sel,read_sel,input wire [15:0] write_data,
 output reg read_hit,output reg [15:0] read_data,
 input wire [1:0] host_ready,host_token,input wire host_fault,
 input wire [11:0] host_words0,host_words1,
 input wire [63:0] host_first0,host_first1,
 output wire host_release,release_bank,release_token,
 output wire ram_read_enable,output wire [13:0] ram_read_halfword,
 input wire ram_read_valid,ram_read_error,input wire [15:0] ram_read_data
);
 wire selecting=host_write && write_sel==8'h30;
 wire controlling=host_write && write_sel==8'h33;
 wire malformed=(selecting && write_data[15]) || (controlling && |write_data[15:2]);
 wire data_ready,exhausted,selected,fault;
 wire [15:0] data;wire [12:0] cursor;
 acq_host_read_window window(
  .clk(clk),.reset(reset),.select(selecting || malformed),
  .select_bank(write_data[13]),.select_token(write_data[14]),
  // An impossible offset routes malformed writes through the same explicit
  // rejection path, preserving the current selection and cursor.
  .select_offset(malformed ? 13'h1fff : write_data[12:0]),
  .pop(host_pop && read_sel==8'h31),
  .release_request(controlling && !malformed && write_data[0]),
  .clear_error(controlling && !malformed && write_data[1]),
  .host_ready(host_ready),.host_token(host_token),.host_fault(host_fault),
  .host_words0(host_words0),.host_words1(host_words1),
  .data_ready(data_ready),.exhausted(exhausted),.data(data),
  .selected(selected),.fault(fault),.cursor(cursor),
  .host_release(host_release),.release_bank(release_bank),.release_token(release_token),
  .ram_read_enable(ram_read_enable),.ram_read_halfword(ram_read_halfword),
  .ram_read_valid(ram_read_valid),.ram_read_error(ram_read_error),.ram_read_data(ram_read_data));
 wire visible0=host_ready[0] && !host_fault,visible1=host_ready[1] && !host_fault;
 always @* begin
  read_hit=1;read_data=0;
  case(read_sel)
   8'h30:read_data={3'b0,cursor};
   8'h31:read_data=data;
   8'h32:read_data={12'b0,selected,fault,exhausted,data_ready};
   8'h34:read_data={11'b0,host_fault,host_token,host_ready};
   8'h35:read_data=visible0 ? {4'b0,host_words0} : 16'b0;
   8'h36:read_data=visible1 ? {4'b0,host_words1} : 16'b0;
   8'h38,8'h39,8'h3a,8'h3b:read_data=visible0 ? host_first0[16*read_sel[1:0]+:16] : 16'b0;
   8'h3c,8'h3d,8'h3e,8'h3f:read_data=visible1 ? host_first1[16*read_sel[1:0]+:16] : 16'b0;
   default:read_hit=0;
  endcase
 end
endmodule
