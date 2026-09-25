// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
// Exact integer moments for one reference/candidate correlation window.
// For N accepted 8-bit pairs, normalized correlation can be derived from
// N*sum_xy-sum_x*sum_y and the two centered energies. No division, square
// root, peak selection, segment validation or stack accumulation occurs here.
// start aborts any previous window; feed begins on a later ready clock.
// last accompanies the final accepted pair. done qualifies the held result.
// Counter overflow invalidates the whole window until reset or another start.
// TRLC-LINKS: REQ-SDS-141
module stack_moments #(parameter COUNT_BITS=32)(
 input wire clk,reset,start,valid,last,
 input wire [7:0] x,y,
 output wire ready,
 output reg busy=0,done=0,overflow=0,
 output reg [COUNT_BITS-1:0] count=0,
 output reg [COUNT_BITS+7:0] sum_x=0,sum_y=0,
 output reg [COUNT_BITS+15:0] sum_xx=0,sum_yy=0,sum_xy=0
);
 reg closing=0,p_valid=0,p_last=0;
 reg [7:0] p_x=0,p_y=0;
 reg [15:0] p_xx=0,p_yy=0,p_xy=0;
 assign ready=busy && !closing && !reset && !start && !overflow;
 always @(posedge clk)begin
  done<=0;
  if(reset || start)begin
   busy<=start && !reset;closing<=0;overflow<=0;
   p_valid<=0;p_last<=0;
   count<=0;sum_x<=0;sum_y<=0;sum_xx<=0;sum_yy<=0;sum_xy<=0;
  end else begin
   p_valid<=valid && ready;
   if(valid && ready)begin
    p_x<=x;p_y<=y;p_xx<=x*x;p_yy<=y*y;p_xy<=x*y;p_last<=last;
    if(last)closing<=1;
   end
   if(p_valid)begin
    if(&count)begin
     overflow<=1;busy<=0;closing<=1;p_valid<=0;
    end else begin
     count<=count+1'b1;sum_x<=sum_x+p_x;sum_y<=sum_y+p_y;
     sum_xx<=sum_xx+p_xx;sum_yy<=sum_yy+p_yy;sum_xy<=sum_xy+p_xy;
     if(p_last)begin busy<=0;done<=1;end
    end
   end
  end
 end
endmodule
