// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
`timescale 1ns/1ps
// TRLC-LINKS: REQ-SDS-040
module tb_cic16_equivalence;
 reg clk=0;always #2 clk=~clk;
 reg enable=0,valid=0;reg [7:0] a=0,b=0;
 wire out_valid,ref_valid;wire [15:0] q,ref_q;
 cic16_pair dut(.*);
 reference_cic16_pair reference(.clk(clk),.enable(enable),.valid(valid),
  .a(a),.b(b),.out_valid(ref_valid),.q(ref_q));
 integer checked=0,epoch,i;reg [31:0] rng=32'ha16c7319;
 always @(posedge clk)begin
  #0.1;
  if(out_valid!==ref_valid)$fatal(1,"valid latency differs in epoch %0d",epoch);
  if(out_valid)begin
   if(q!==ref_q)$fatal(1,"Q8 differs epoch=%0d expected=%h actual=%h",epoch,ref_q,q);
   checked=checked+1;
  end
 end
 initial begin
  for(epoch=0;epoch<8;epoch=epoch+1)begin
   @(negedge clk);enable=0;valid=0;
   repeat(epoch%3+1)@(negedge clk);
   enable=1;
   for(i=0;i<100000;i=i+1)begin
    rng=rng^(rng<<13);rng=rng^(rng>>17);rng=rng^(rng<<5);
    valid=epoch<4 || rng[2:0]!=0;
    case(epoch)
     0:begin a=0;b=0;end
     1:begin a=255;b=255;end
     2:begin a=0;b=255;end
     3:begin a=100;b=101;end
     4:begin a=i%1000==0 ? 255 : 128;b=128;end
     default:begin a=rng[7:0];b=rng[15:8];end
    endcase
    @(negedge clk);
   end
   valid=0;repeat(30)@(negedge clk);
  end
  if(checked<80000)$fatal(1,"insufficient compared outputs %0d",checked);
  $display("PASS CIC20 vs frozen CIC28: %0d cycle-exact Q8 outputs, extremes, fractions, stalls and short resets",checked);
  $finish;
 end
 initial begin #4000000;$fatal(1,"timeout");end
endmodule
