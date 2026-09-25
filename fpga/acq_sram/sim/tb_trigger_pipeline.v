// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
`timescale 1ns/1ps
// TRLC-LINKS: REQ-SDS-184
module tb_trigger_pipeline;
 reg clk=0;always #2 clk=~clk;
 reg enable=0,valid=0,precision_mode=0,channel=0,falling=0,force_trigger=0,match_trigger=0;
 reg [31:0] data=0;reg [15:0] level=0;reg [1:0] mode=0;
 wire [31:0] out_data;wire out_valid,event_trigger,second;
 acquisition_trigger_pipeline dut(.*);
 reg [31:0] words[0:4095];reg events[0:4095],seconds[0:4095];
 integer head=0,tail=0,total=0,epoch,i;reg [15:0] previous=0,first_value,second_value;
 reg previous_valid=0,first_edge,second_edge;reg [31:0] rng=32'h936bd701;
 always @(posedge clk)begin
  if(!enable)begin head=0;tail=0;previous_valid=0;end
  else begin
   if(out_valid)begin
    if(head==tail || out_data!==words[head] || event_trigger!==events[head] || second!==seconds[head])
     $fatal(1,"epoch %0d output %0d data=%h expected=%h event=%b/%b second=%b/%b",epoch,head,out_data,words[head],event_trigger,events[head],second,seconds[head]);
    head=head+1;total=total+1;
   end
   if(valid)begin
    first_value=precision_mode ? (channel ? data[31:16] : data[15:0]) : {channel ? data[15:8] : data[7:0],8'b0};
    second_value={channel ? data[31:24] : data[23:16],8'b0};
    first_edge=previous_valid && (falling ? previous>level && first_value<=level : previous<level && first_value>=level);
    second_edge=!precision_mode && (falling ? first_value>level && second_value<=level : first_value<level && second_value>=level);
    words[tail]=data;events[tail]=force_trigger || mode==0 || (mode==1 && (first_edge || second_edge)) || (mode==2 && match_trigger);
    seconds[tail]=mode==1 && !force_trigger && !first_edge && second_edge;
    tail=tail+1;previous_valid=1;previous=precision_mode ? first_value : second_value;
   end
  end
 end
 initial begin
  for(epoch=0;epoch<96;epoch=epoch+1)begin
   @(negedge clk);enable=0;valid=0;
   @(negedge clk);precision_mode=epoch%2;channel=(epoch/2)%2;falling=(epoch/4)%2;mode=(epoch/8)%3;
   case(epoch/24)0:level=0;1:level=16'h8000;2:level=16'h80a7;3:level=16'hffff;endcase
   enable=1;
   for(i=0;i<1024;i=i+1)begin
    @(negedge clk);rng=rng^(rng<<13);rng=rng^(rng>>17);rng=rng^(rng<<5);
    data=rng;valid=i%11<8;force_trigger=i%37==0;match_trigger=i%7==0;
    if(i%9==0)data={level,level};
    if(i%13==0)data=0;
    if(i%17==0)data=32'hffffffff;
   end
   @(negedge clk);valid=0;repeat(4)@(negedge clk);
   if(head!=tail)$fatal(1,"lost pipeline words");
   // Abort while both stages hold data, then change settings next epoch.
   valid=1;repeat(2)@(negedge clk);enable=0;valid=0;
  end
  $display("PASS trigger pipeline: %0d checked words across 96 raw/precision/channel/direction/mode/threshold epochs, bubbles and aborts",total);$finish;
 end
endmodule
