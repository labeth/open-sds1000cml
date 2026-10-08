`timescale 1ns/1ps
// panel_scan against a behavioural 64-bit parallel-load shift chain
// (74HC165-like: PL low loads, the next bit appears after each rising CP).
// TRLC-LINKS: REQ-SDS-022
module tb_panel_scan;
 reg clk=0;always #6.25 clk=~clk;
 reg [15:0] cfg=0,half=4,gap=1;
 wire f2,j2,f1,j1_level;wire [63:0] frame,knobs;wire [7:0] frames,changes;
 reg [63:0] keys=64'hF0E1_D2C3_B4A5_9687,chain=0;
 wire j1=chain[63];
 panel_scan dut(.clk(clk),.cfg(cfg),.half(half),.gap(gap),.j1(j1),.load(f2),.sclk(j2),.f1(f1),
  .frame(frame),.knobs(knobs),.frames(frames),.changes(changes),.j1_level(j1_level));
 always @(posedge j2) if(f2) chain<={chain[62:0],1'b1};
 always @* if(!f2) chain=keys;
 integer errors=0;
 task expect_frame(input [63:0] want);
  integer k;reg [63:0] got;
  begin
   for(k=0;k<64;k=k+1)got[k]=frame[k];
   // First shifted bit is chain[63] = keys[63].
   for(k=0;k<64;k=k+1)if(got[k]!==want[63-k])begin errors=errors+1;end
   if(got!=={want[0],want[1],want[2],want[3],want[4],want[5],want[6],want[7],want[8],want[9],want[10],want[11],want[12],want[13],want[14],want[15],
            want[16],want[17],want[18],want[19],want[20],want[21],want[22],want[23],want[24],want[25],want[26],want[27],want[28],want[29],want[30],want[31],
            want[32],want[33],want[34],want[35],want[36],want[37],want[38],want[39],want[40],want[41],want[42],want[43],want[44],want[45],want[46],want[47],
            want[48],want[49],want[50],want[51],want[52],want[53],want[54],want[55],want[56],want[57],want[58],want[59],want[60],want[61],want[62],want[63]})
    $display("FAIL frame %h want reversed %h",got,want);
  end
 endtask
 initial begin
  #200;
  if({f1,f2,j2}!==3'b111)begin $display("FAIL idle pins %b",{f1,f2,j2});errors=errors+1;end
  // Enable: load active low, J2 idles low, sample before the rising edge,
  // F1 low pulse on change, 64 bits.
  cfg=16'h8000|(2<<10);
  wait(frames==2);
  expect_frame(keys);
  if(changes!==1)begin $display("FAIL changes %0d",changes);errors=errors+1;end
  keys[0]=0;
  wait(frames==5);
  expect_frame(keys);
  if(changes!==2)begin $display("FAIL changes after key %0d",changes);errors=errors+1;end
  // Knob 0 (bits 0,1 of the first shifted byte = keys[63:62] reversed):
  // three steps forward 00->01->11->10 then one back, one per frame.
  keys[63]=0;keys[62]=0;wait(frames==7);
  keys[63]=1;wait(frames==9);
  keys[62]=1;wait(frames==11);
  keys[63]=0;wait(frames==13);
  keys[63]=1;wait(frames==15);
  if(knobs[7:0]!==8'd2)begin $display("FAIL knob count %0d",knobs[7:0]);errors=errors+1;end
  cfg=0;#100;
  if({f2,j2}!==2'b11)begin $display("FAIL disable pins");errors=errors+1;end
  if(errors==0)$display("PASS panel_scan");
  $finish;
 end
 reg f1_seen_low=0;always @(negedge f1) f1_seen_low=1;
 initial #2000000 begin $display("FAIL timeout frames=%0d",frames);$finish;end
 always @(posedge clk) if(frames==5 && !f1_seen_low)begin $display("FAIL no F1 pulse");errors=errors+1;f1_seen_low=1;end
endmodule
