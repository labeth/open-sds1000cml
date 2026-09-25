// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-HOST-FIFO
// Small register-backed asynchronous FIFO for the 250 MHz host pair packer.
// RAM slots must stay in logic: M9K cannot meet the 250 MHz source period.
// A COMMON reset discards both sides; assertion is asynchronous and each side
// releases synchronously. Independent reset of only one side is unsupported.
// push is a non-retry offer: push while full outside reset latches overflow
// and drops that offer. Offers during reset/release are discarded. Destination uses ordinary valid/ready, with a registered output.
// TRLC-LINKS: REQ-SDS-050, REQ-SDS-078
module sram_host_fifo #(parameter WIDTH=80,ADDR_BITS=3)(
 input wire reset,source_clk,dest_clk,
 input wire push,input wire [WIDTH-1:0] source_data,
 output wire source_ready,output reg overflow=0,
 output reg dest_valid=0,output reg [WIDTH-1:0] dest_data=0,
 input wire dest_ready
);
 localparam DEPTH=1<<ADDR_BITS;
 (* async_reg="true" *) reg [1:0] source_reset=3,dest_reset=3;
 always @(posedge source_clk or posedge reset)
  if(reset)source_reset<=3;else source_reset<={source_reset[0],1'b0};
 always @(posedge dest_clk or posedge reset)
  if(reset)dest_reset<=3;else dest_reset<={dest_reset[0],1'b0};
 (* ramstyle="logic" *) reg [WIDTH-1:0] slots[0:DEPTH-1];
 reg [ADDR_BITS:0] wbin=0,wgray=0,rbin=0,rgray=0;
 (* async_reg="true",preserve *) reg [ADDR_BITS:0] rgray_meta=0,rgray_sync=0;
 (* async_reg="true",preserve *) reg [ADDR_BITS:0] wgray_meta=0,wgray_sync=0;
 reg full=0,empty=1;
 assign source_ready=!reset && !source_reset[1] && !full;
 wire take_write=push && source_ready;
 wire take_read=!dest_reset[1] && !reset && !empty && (!dest_valid || dest_ready);
 wire [ADDR_BITS:0] wnext=wbin+take_write;
 wire [ADDR_BITS:0] wgray_next=(wnext>>1)^wnext;
 wire [ADDR_BITS:0] rnext=rbin+take_read;
 wire [ADDR_BITS:0] rgray_next=(rnext>>1)^rnext;
 // ADDR_BITS >= 2. Invert the two high Gray bits to detect one full lap.
 wire [ADDR_BITS:0] full_target={~rgray_sync[ADDR_BITS:ADDR_BITS-1],rgray_sync[ADDR_BITS-2:0]};
 // Registered one-hot write location avoids binary decode feeding all
 // WIDTH write enables. Gray/binary pointers still handle occupancy/CDC.
 (* preserve *) reg [DEPTH-1:0] write_slot=1;
 always @(posedge source_clk or posedge source_reset[1])begin
  if(source_reset[1])write_slot<=1;
  else if(take_write)write_slot<={write_slot[DEPTH-2:0],write_slot[DEPTH-1]};
 end
 // Precompute capacity-qualified slot enables alongside the next pointers.
 // Raw/local reset still suppresses every physical write immediately.
 (* preserve *) reg [DEPTH-1:0] writable_slot=1;
 wire [DEPTH-1:0] slot_next=take_write ? {write_slot[DEPTH-2:0],write_slot[DEPTH-1]} : write_slot;
 always @(posedge source_clk or posedge source_reset[1])begin
  if(source_reset[1])writable_slot<=1;
  else writable_slot<=wgray_next==full_target ? {DEPTH{1'b0}} : slot_next;
 end
 genvar slot;
 generate for(slot=0;slot<DEPTH;slot=slot+1)begin: write_slots
  always @(posedge source_clk)if(push && !reset && !source_reset[1] && writable_slot[slot])slots[slot]<=source_data;
 end endgenerate
 always @(posedge source_clk or posedge source_reset[1])begin
  if(source_reset[1])begin wbin<=0;wgray<=0;rgray_meta<=0;rgray_sync<=0;full<=0;overflow<=0;end
  else begin
   rgray_meta<=rgray;rgray_sync<=rgray_meta;
   wbin<=wnext;wgray<=wgray_next;full<=wgray_next==full_target;
   if(push && full)overflow<=1;
  end
 end
 always @(posedge dest_clk or posedge dest_reset[1])begin
  if(dest_reset[1])begin
   rbin<=0;rgray<=0;wgray_meta<=0;wgray_sync<=0;empty<=1;dest_valid<=0;dest_data<=0;
  end else begin
   wgray_meta<=wgray;wgray_sync<=wgray_meta;
   rbin<=rnext;rgray<=rgray_next;empty<=rgray_next==wgray_sync;
   if(take_read)begin dest_data<=slots[rbin[ADDR_BITS-1:0]];dest_valid<=1;end
   else if(dest_ready)dest_valid<=0;
  end
 end
endmodule
