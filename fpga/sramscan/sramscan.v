// sramscan.v — drive register-selected candidate ball, clock D14, read DQ (ID 0xAD0F)
module sramscan(input wire clk, output wire sck_pin, inout wire [116:0] cand, input wire [21:0] dq,
 input wire nCS1,input wire nOE,input wire nWE,input wire [6:0] sel, inout wire [15:0] gpmc_d, output wire gpmc_wait);
 reg [2:0] cs1_q=3'b111,we_q=3'b111; reg [6:0] sel_q2=0,sel_q1=0; reg [15:0] d_q1=0,d_q2=0;
 always @(posedge clk) begin cs1_q<={cs1_q[1:0],nCS1}; we_q<={we_q[1:0],nWE}; sel_q1<=sel; sel_q2<=sel_q1; d_q1<=gpmc_d; d_q2<=d_q1; end
 wire cs1_low=(cs1_q[2]==0); wire we_commit=(we_q[2]==0)&&(we_q[1]==1);
 wire [7:0] wr_sel={1'b0,sel_q2[6:2],2'b00}; wire [7:0] rd_sel={1'b0,sel[6:2],2'b00};
 reg [8:0] dsel=9'd511; reg dlvl=0; reg [15:0] clkdiv=16'd25; reg [7:0] np=0; reg pulsing=0; reg sck=0; reg [15:0] dv=0;
 wire startp=we_commit&&cs1_low&&(wr_sel==8'h34);
 always @(posedge clk) if(we_commit&&cs1_low) case(wr_sel) 8'h20:dsel<=d_q2[8:0]; 8'h24:dlvl<=d_q2[0]; 8'h30:clkdiv<=d_q2; default:; endcase
 always @(posedge clk) begin if(startp) begin np<=d_q2[7:0]; pulsing<=(d_q2[7:0]!=0); dv<=0; sck<=0; end
  else if(pulsing) begin if(dv>=clkdiv) begin dv<=0; sck<=~sck; if(sck) begin if(np<=1) pulsing<=0; else np<=np-8'd1; end end else dv<=dv+16'd1; end end
 assign sck_pin=sck;
 genvar i; generate for(i=0;i<117;i=i+1) begin: dr assign cand[i]=(dsel==i)?dlvl:1'bz; end endgenerate
 reg [21:0] dqlive=0; always @(posedge clk) dqlive<=dq;
 reg [15:0] rdata; always @* case(rd_sel) 8'h10:rdata=16'hAD0F; 8'h54:rdata=dqlive[15:0]; 8'h58:rdata={10'd0,dqlive[21:16]}; default:rdata=0; endcase
 wire ra=(~nCS1)&(~nOE); assign gpmc_d=ra?rdata:16'hzzzz; assign gpmc_wait=1'b1;
endmodule
