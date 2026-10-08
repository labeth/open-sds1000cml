// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
`timescale 1ns/1ps
// TRLC-LINKS: REQ-SDS-141
module tb_stack_bin_writer;
 reg clk=0;always #4 clk=~clk;
 reg reset=1,valid=0,odd=0;wire ready;
 reg [31:0] bin=0;reg [1:0] mask=0;reg [36:0] value0=0,value1=0;
 wire request_valid,request_write,request_channel;wire [31:0] request_bin;
 reg request_ready=0,response_valid=0,response_error=0;wire response_ready;
 wire [68:0] write_sum,write_sum_a;wire [105:0] write_sum2;wire [31:0] write_count,write_count_a;
 reg [68:0] read_sum=0,read_sum_a=0;reg [105:0] read_sum2=0;reg [31:0] read_count=0,read_count_a=0;
 wire done,fault;stack_bin_writer dut(.*);
 reg [68:0] sums[0:15],sums_a[0:15];reg [105:0] squares[0:15];reg [31:0] counts[0:15],counts_a[0:15];
 integer tick=0,pending=0,delay=0,operations=0,fail_at=0,address=0,i,j,cycles,expected_ops=0;
 reg wr=0,err=0;reg [68:0] ws=0,wa=0;reg [105:0] wq=0;reg [31:0] wc=0,wca=0;
 reg held=0;reg [341:0] held_request;
 always @(posedge clk)begin
  tick<=tick+1;request_ready<=tick%5!=0 && tick%5!=1;
  if(reset)begin
   pending<=0;response_valid<=0;operations<=0;held<=0;
   for(i=0;i<16;i=i+1)begin sums[i]<=0;sums_a[i]<=0;squares[i]<=0;counts[i]<=0;counts_a[i]<=0;end
  end else begin
   if(held && (!request_valid || held_request!=={request_write,request_bin,request_channel,write_sum,write_sum2,write_count,write_sum_a,write_count_a}))$fatal(1,"request changed under stall");
   held<=request_valid && !request_ready;
   held_request<={request_write,request_bin,request_channel,write_sum,write_sum2,write_count,write_sum_a,write_count_a};
   if(request_valid && request_ready)begin
    if(pending || response_valid)$fatal(1,"overlapping operation");
    if(request_bin>=8)$fatal(1,"address");
    pending<=1;delay<=tick%7+1;operations<=operations+1;
    address<=request_bin*2+request_channel;wr<=request_write;err<=operations+1==fail_at;
    ws<=write_sum;wq<=write_sum2;wc<=write_count;wa<=write_sum_a;wca<=write_count_a;
   end
   if(pending)begin
    if(delay!=0)delay<=delay-1;
    else begin
     response_valid<=1;response_error<=err;pending<=0;
     read_sum<=sums[address];read_sum2<=squares[address];read_count<=counts[address];read_sum_a<=sums_a[address];read_count_a<=counts_a[address];
     if(wr && !err)begin sums[address]<=ws;squares[address]<=wq;counts[address]<=wc;sums_a[address]<=wa;counts_a[address]<=wca;end
    end
   end
   if(response_valid && response_ready)response_valid<=0;
  end
 end
 task send;
 begin
  valid=1;cycles=0;#1;
  while(!ready)begin @(negedge clk);cycles=cycles+1;if(cycles>1000)$fatal(1,"input stall");end
  @(negedge clk);valid=0;bin=32'hffffffff;mask=0;odd=0;value0=0;value1=0;
  cycles=0;while(!done && !fault)begin @(negedge clk);cycles=cycles+1;if(cycles>1000)$fatal(1,"completion");end
 end
 endtask
 reg [307:0] before_overflow;
 integer fd,r;reg [4095:0] file_name;
 initial begin
  if(!$value$plusargs("input=%s",file_name))$fatal(1,"input");
  fd=$fopen(file_name,"r");repeat(3)@(negedge clk);reset=0;
  while(!$feof(fd))begin
   r=$fscanf(fd,"%d %d %d %d %d\n",bin,mask,odd,value0,value1);if(r!=5)$fatal(1,"fixture");
   expected_ops=expected_ops+2*mask[0]+2*mask[1];send();if(fault)$fatal(1,"unexpected fault");@(negedge clk);
  end
  if(operations!=expected_ops)$fatal(1,"masked channel access");
  for(j=0;j<16;j=j+1)$display("B %0d %0d %0d %0d %0d %0d",j,sums[j],squares[j],counts[j],sums_a[j],counts_a[j]);
  for(j=1;j<=7;j=j+1)begin
   reset=1;@(negedge clk);reset=0;fail_at=j<=2 ? j:0;
   case(j)
    3:counts[0]=32'hffffffff;
    4:sums[0]={69{1'b1}};
    5:squares[0]={106{1'b1}};
    6:sums_a[0]={69{1'b1}};
    7:counts_a[0]=32'hffffffff;
   endcase
   before_overflow={sums[0],squares[0],counts[0],sums_a[0],counts_a[0]};
   bin=0;mask=j>=3 ? 3:1;odd=1;value0=37'h1fffffffff;send();if(!fault)$fatal(1,"missing failure");
   if(operations!=(j==2 ? 2:1))$fatal(1,"failure operation count");
   if(j>=3 && before_overflow!=={sums[0],squares[0],counts[0],sums_a[0],counts_a[0]})
    $fatal(1,"overflow changed retained state field=%0d",j);
   repeat(30)begin @(negedge clk);if(request_valid || ready || done)$fatal(1,"fault not sticky");end
  end
  reset=1;@(negedge clk);reset=0;fail_at=0;
  bin=0;mask=3;odd=1;value0=10;value1=20;send();if(fault || !done)$fatal(1,"reset recovery");
  if(sums[0]!=10 || sums[1]!=20 || counts[0]!=1 || counts[1]!=1)$fatal(1,"recovery state");
  $display("PASS fault and recovery checks");$finish;
 end
endmodule
