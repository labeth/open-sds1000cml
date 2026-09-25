// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
`timescale 1ns/1ps
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018, REQ-SDS-039
module tb_usbls_trigger #(parameter EXTERNAL_RAM=1);
 reg clk=0;always #4 clk=~clk;
 reg reset=1,enable=1,line=1,inverted=0;
 reg [23:0] ticks=20*256;
 reg [31:0] pattern=32'hff55;reg [2:0] length=2;
 reg [63:0] sample=64'hffffff00;
 always @(posedge clk)sample<=sample+4;
 wire hit,valid;wire [7:0] kind,data;wire [15:0] count;wire [63:0] stamp;
 wire scratch_write;wire [3:0] scratch_wr,scratch_rd;wire [63:0] scratch_data,scratch_q;
 usbls_trigger #(.EXTERNAL_RAM(EXTERNAL_RAM)) dut(.clk(clk),.reset(reset),.enable(enable),.tick(1'b1),.line(line^inverted),.inverted(inverted),
  .bit_ticks_q8(ticks),.pattern(pattern),.pattern_len(length),.sample(sample),.match(hit),
  .event_valid(valid),.event_kind(kind),.event_data(data),.event_count(count),.event_sample(stamp),
  .scratch_write(scratch_write),.scratch_wr(scratch_wr),.scratch_rd(scratch_rd),.scratch_data(scratch_data),.scratch_q(scratch_q));
 protocol_scratch ram(.clk(clk),.reset(reset),.manchester(1'b0),
  .usb_write(scratch_write),.usb_wr(scratch_wr),.usb_rd(scratch_rd),.usb_data(scratch_data),.usb_q(scratch_q),
  .man_write(2'd0),.man_wr_a(8'd0),.man_wr_b(8'd0),.man_rd(8'd0),
  .man_data_a(33'd0),.man_data_b(33'd0),.man_q_a(),.man_q_b());
 integer starts=0,ends=0,errors=0,hits=0,received=0,expected=0;
 reg [7:0] want[0:4095];reg checking=1;
 reg [63:0] last_stamp=0;
 always @(negedge clk)begin
  if(hit)hits=hits+1;
  if(valid)begin
   if(stamp>sample || stamp<last_stamp)$fatal(1,"event ordinal out of order %h %h",stamp,last_stamp);
   last_stamp=stamp;
   case(kind)
    1:starts=starts+1;
    2:begin
     if(checking && (received>=expected || data!==want[received]))$fatal(1,"USB payload %0d got %h expected %h",received,data,want[received]);
     received=received+1;
    end
    3:ends=ends+1;
    4:errors=errors+1;
    default:$fatal(1,"invalid USB event kind");
   endcase
  end
 end
 integer phase=0,runones=0,wire_ticks=0;reg nrzi=1;
 task drive_cell(input reg value);
 integer clocks;
 begin
  line=value;phase=phase+(wire_ticks==0?ticks:wire_ticks);clocks=phase>>8;phase=phase&255;
  repeat(clocks)@(negedge clk);
 end endtask
 task rawbit(input reg value);
 begin if(!value)nrzi=~nrzi;drive_cell(nrzi);end endtask
 task octet(input [7:0] value,input reg badstuff);
 integer b;
 begin
  for(b=0;b<8;b=b+1)begin
   rawbit(value[b]);runones=value[b]?runones+1:0;
   if(runones==6 && !badstuff)begin rawbit(0);runones=0;end
  end
 end endtask
 // Payload writes precede transmission; use a fixed base since receiver runs concurrently.
 task good_packet(input integer n);
 integer b,base;
 begin
  base=expected;for(b=0;b<n;b=b+1)want[base+b]=(b%4==0)?8'hff:(b%4==1)?8'h55:(b%4==2)?8'hf8:8'h00;
  expected=expected+n;nrzi=1;runones=0;octet(8'h80,0);octet(8'hc3,0);
  for(b=0;b<n;b=b+1)octet(want[base+b],0);
  drive_cell(0);drive_cell(0);repeat(16)drive_cell(1);
 end endtask
 integer oldhits,olderrors;
 initial begin
  repeat(8)@(negedge clk);reset=0;
  // An epoch may begin inside a packet. Do not admit a SYNC-looking payload
  // until a complete idle delimiter has established framing.
  checking=0;good_packet(8);checking=1;
  if(starts!=0 || ends!=0 || hits!=0)$fatal(1,"USB accepted an unframed startup packet");
  expected=0;received=0;repeat(16)drive_cell(1);
  good_packet(20);
  if(received!=expected || starts!=1 || ends!=1 || hits!=1 || errors!=0)$fatal(1,"integer packet failed");
  ticks=21333;inverted=1;repeat(16)drive_cell(1);good_packet(96);
  if(received!=expected || hits!=2 || errors!=0)$fatal(1,"1.5 MHz fractional/inverted packet failed");
  ticks=2667;repeat(16)drive_cell(1);good_packet(96);
  if(received!=expected || hits!=3 || errors!=0)$fatal(1,"12 MHz fractional packet failed");
  wire_ticks=2688;good_packet(1024);wire_ticks=0;
  if(received!=expected || hits!=4 || errors!=0)$fatal(1,"long fractional packet with clock mismatch failed");
  pattern=32'hdead;oldhits=hits;good_packet(16);
  if(hits!=oldhits || received!=expected)$fatal(1,"absent pattern triggered");
  length=0;nrzi=1;runones=0;octet(8'h80,0);octet(8'hd2,0);drive_cell(0);drive_cell(0);repeat(16)drive_cell(1);
  if(hits!=oldhits)$fatal(1,"empty handshake matched an any-payload predicate");
  oldhits=hits;olderrors=errors;nrzi=1;runones=0;octet(8'h80,0);octet(8'hc2,0);drive_cell(0);drive_cell(0);repeat(16)drive_cell(1);
  if(errors!=olderrors+1 || hits!=oldhits)$fatal(1,"bad PID accepted");
  olderrors=errors;checking=0;nrzi=1;runones=0;octet(8'h80,0);octet(8'hc3,0);octet(8'h00,0);octet(8'hff,1);octet(8'h00,1);drive_cell(0);drive_cell(0);repeat(16)drive_cell(1);
  if(errors!=olderrors+1 || hits!=oldhits)$fatal(1,"stuff violation accepted");
  // Partial capture/disable cannot leak a pending match or a previous payload.
  reset=1;repeat(8)@(negedge clk);reset=0;repeat(16)drive_cell(1);
  nrzi=1;runones=0;octet(8'h80,0);octet(8'hc3,0);enable=0;repeat(16)drive_cell(1);
  enable=1;repeat(16)drive_cell(1);checking=1;received=0;expected=0;length=2;pattern=32'hff55;good_packet(8);
  if(received!=expected || hits!=oldhits+1)$fatal(1,"disable recovery failed");
  $display("PASS USB SYNC/PID, stuffing, EOP exclusion, fractional LS/FS, polarity, predicates, errors and reset recovery");$finish;
 end
 initial begin #10000000;$fatal(1,"timeout");end
endmodule

// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018, REQ-SDS-039
module tb_usbls_overflow;
 reg clk=0,reset=1,line=1;always #4 clk=~clk;
 reg [63:0] sample=0,last=0;always @(posedge clk)sample<=sample+4;
 wire hit,valid;wire [7:0] kind,data;wire [15:0] count;wire [63:0] stamp;
 usbls_trigger dut(.clk(clk),.reset(reset),.enable(1'b1),.tick(1'b1),.line(line),.inverted(1'b0),.bit_ticks_q8(24'd2048),.pattern(32'd0),.pattern_len(3'd0),.sample(sample),.match(hit),.event_valid(valid),.event_kind(kind),.event_data(data),.event_count(count),.event_sample(stamp));
 integer errors=0,hits=0,n;
 always @(negedge clk)begin
  if(hit)hits=hits+1;
  if(valid)begin
   if(stamp<last)$fatal(1,"oversized packet emitted a backwards event after ERROR");last=stamp;
   if(kind==4)errors=errors+1;
  end
 end
 task rawbit(input reg b);begin if(!b)line=~line;repeat(8)@(negedge clk);end endtask
 task octet(input [7:0] b);integer i;begin for(i=0;i<8;i=i+1)rawbit(b[i]);end endtask
 initial begin repeat(8)@(negedge clk);reset=0;repeat(100)@(negedge clk);
 octet(8'h80);octet(8'hc3);for(n=0;n<8300;n=n+1)octet(0);
 line=0;repeat(16)@(negedge clk);line=1;repeat(200)@(negedge clk);
 if(errors!=1||hits!=0)$fatal(1,"oversized packet accepted errors=%0d hits=%0d",errors,hits);
 $display("PASS oversized USB packet error and monotonic event ordinals");$finish;end
endmodule
