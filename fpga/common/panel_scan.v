// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
// Front-panel serial scan (ADR-PANEL-SERIAL-SCAN). The panel is a
// parallel-load shift chain: load (ball J2) latches it, sclk (ball F2) clocks
// 64 bits out and they arrive on J1, first bit first. The factory image runs
// F2 as a free clock of about 300 kHz and J2 as the frame level, changing
// every 64 clocks. Bench-settled on 2026-09-27: J2 high loads, F2 idles low,
// J1 sampled before the rising edge; keys and knob phases read active low,
// and each byte carries one knob's quadrature pair in bits 0 and 1. F1 is the
// ARM's key interrupt (gpio3_19): a low pulse per changed frame raises it.
//
// cfg[15] enable; cfg[14] load level (J2 during the load); cfg[13] sample J1
// at the end of the sclk-high half (else at the end of the low half);
// cfg[12] sclk idle level; cfg[11:10] F1: 0 high, 1 low, 2 low pulse on a
// changed frame, 3 high pulse. half: sclk half period in clocks (>= 1). gap:
// idle half periods between frames. Disabled, all three pins idle high, as
// the images held them before. knobs holds one wrapping 8-bit quadrature
// count per byte (knob n in bits 8n+7..8n), one count per phase transition.
// TRLC-LINKS: REQ-SDS-022
module panel_scan(
 input wire clk,
 input wire [15:0] cfg,half,gap,
 input wire j1,
 output reg load=1,sclk=1,f1=1,
 output reg [63:0] frame=0,
 output reg [63:0] knobs=0,
 output reg [7:0] frames=0,changes=0,
 output wire j1_level
);
 reg [1:0] j1_sync=2'b11;
 assign j1_level=j1_sync[1];
 wire enable=cfg[15],load_level=cfg[14],late=cfg[13],idle=cfg[12];
 wire [1:0] f1_mode=cfg[11:10];
 localparam IDLE=0,LOAD=1,LOW=2,HIGH=3,DONE=4,KNOBS=5;
 reg [2:0] state=IDLE;
 // One strobe per sclk half period from a reloading down-counter.
 reg [15:0] tick=0;
 reg step=0;
 reg [15:0] count=0;
 reg [63:0] shift=0;
 reg [7:0] pulse=0;
 reg [15:0] phase=0;
 reg primed=0;
 integer n;
 always @(posedge clk)begin
  j1_sync<={j1_sync[0],j1};
  step<=tick==0;
  tick<=tick==0 ? half-1'b1 : tick-1'b1;
  if(step && pulse!=0)pulse<=pulse-1'b1;
  case(f1_mode)
   0:f1<=1;1:f1<=0;
   2:f1<=pulse==0;3:f1<=pulse!=0;
  endcase
  if(!enable)begin
   state<=IDLE;load<=1;sclk<=1;count<=0;primed<=0;
  end else if(state==KNOBS)begin
   // Gray-code step per knob: 00,01,11,10 counts up. A skipped state (both
   // bits changed) is dropped; the first frame only sets the baseline.
   for(n=0;n<8;n=n+1)
    if(primed && (frame[8*n]^phase[2*n])^(frame[8*n+1]^phase[2*n+1]))
     knobs[8*n+:8]<=knobs[8*n+:8]+((phase[2*n]^frame[8*n+1]) ? 8'hff : 8'h01);
   for(n=0;n<8;n=n+1)phase[2*n+:2]<={frame[8*n+1],frame[8*n]};
   primed<=1;state<=IDLE;
  end else if(step)case(state)
   IDLE:begin
    load<=!load_level;sclk<=idle;
    if(count>=gap)begin count<=0;state<=LOAD;end
    else count<=count+1'b1;
   end
   // Two half periods of load, then the first bit is on J1.
   LOAD:begin
    load<=load_level;sclk<=idle;
    if(count[0])begin count<=0;load<=!load_level;state<=LOW;end
    else count<=1;
   end
   LOW:begin
    if(!late)shift<={j1_sync[1],shift[63:1]};
    sclk<=!idle;state<=HIGH;
   end
   HIGH:begin
    if(late)shift<={j1_sync[1],shift[63:1]};
    sclk<=idle;
    if(count==63)begin count<=0;state<=DONE;end
    else begin count<=count+1'b1;state<=LOW;end
   end
   DONE:begin
    frames<=frames+1'b1;
    if(shift!=frame)begin changes<=changes+1'b1;pulse<=8'd255;end
    frame<=shift;state<=KNOBS;
   end
  endcase
 end
endmodule
