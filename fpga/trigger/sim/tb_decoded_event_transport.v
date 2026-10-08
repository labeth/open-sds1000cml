// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
`timescale 1ns/1ps
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-058
module tb_decoded_event_transport #(
 parameter AW=1,HOST_AW=2,BURST=20,HOST_PAUSE=10,EXPECT_FULL=0
);
 reg sc=0,hc=0;always #4 sc=~sc;always #7 hc=~hc;
 reg reset=1,iv=0,pop=0;reg [31:0] epoch=9,value=0;
 reg [63:0] sample=0;
 wire available,error,overflow;wire [3:0] cursor;wire [15:0] data,complete_count;
 decoded_event_transport #(.AW(AW),.HOST_AW(HOST_AW)) dut(
  reset,sc,hc,epoch,iv,sample,8'd2,8'd8,8'd5,32'd0,value,32'hfedcba98,
  pop,1'b0,1'b0,available,error,cursor,data,overflow,complete_count);
 integer i,j,seen=0,lost=0,seq=0,output_file;
 reg [255:0] record;
 reg [1023:0] output_path;
// TRLC-LINKS: REQ-SDS-013
 task read_event;begin
  wait(available);
  for(j=0;j<16;j=j+1)begin
   @(negedge hc);
   if(!available || complete_count==0 || cursor!=j || error)$fatal(1,"readout lost event at halfword %0d",j);
   record[16*j+:16]=data;pop=1;
   @(posedge hc);#1;pop=0;
  end
  if(record[7:0]!=1 || record[63:32]!=epoch || record[95:64]!=seq)$fatal(1,"header/epoch/sequence mismatch");
  seq=seq+1;
  case(record[15:8])
   2:begin
    if(record[23:16]!=8 || record[31:24]!=5 || record[223:192]!=(32'h80000000+seen+lost) || record[255:224]!=32'hfedcba98 || record[159:96]!=(64'h100000000+seen+lost))
     $fatal(1,"decoded data or timestamp reordered %h",record);
    seen=seen+1;
   end
   5:begin
    if(record[255:224]==0 || record[159:96]!=(64'h100000000+seen+lost))$fatal(1,"loss count or first timestamp wrong");
    lost=lost+record[255:224];
   end
   default:$fatal(1,"unexpected kind");
  endcase
  if(output_file)for(j=0;j<32;j=j+1)$fwrite(output_file,"%c",record[8*j+:8]);
 end endtask
 initial begin
  output_file=0;
  if($value$plusargs("output=%s",output_path))output_file=$fopen(output_path,"wb");
  #25;reset=0;repeat(5)@(negedge sc);
  // No host reads during the burst: it must cause explicitly accounted loss.
  for(i=0;i<BURST;i=i+1)begin iv=1;value=32'h80000000+i;sample=64'h100000000+i;@(negedge sc);end
  iv=0;repeat(HOST_PAUSE)@(negedge hc);
  if(!overflow)$fatal(1,"burst did not exercise overflow");
  if(EXPECT_FULL && complete_count!=(1<<HOST_AW))$fatal(1,"host pause did not fill the production reader");
  while(seen+lost<BURST)read_event;
  if(lost==0 || seen==0 || seen+lost!=BURST)$fatal(1,"input events not conserved");
  repeat(20)@(negedge hc);
  if(available)$fatal(1,"duplicate event after drain");
  if(output_file)$fclose(output_file);
  $display("PASS event transport: %0d delivered + %0d explicitly lost = %0d",seen,lost,BURST);$finish;
 end
 initial begin #5000000;$fatal(1,"timeout");end
endmodule
