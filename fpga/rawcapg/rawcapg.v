// rawcapg.v — GPMC-line finder (auto-generated). 24 candidate balls.
// Snapshots the candidate balls on each GPMC write-commit so a driven address/data pattern
// reveals which balls are CPU-interface lines: vary the write address -> address lines change;
// vary the write data -> data lines mirror it; constant -> not a GPMC line.
module rawcapg (
    input  wire        clk,
    input  wire [23:0] probe,
    output wire [7:0]  adc_enc, output wire [3:0] adc_ctl_hi, output wire [2:0] adc_ctl_lo,
    input  wire        nCS1, input wire nOE, input wire nWE,
    input  wire [6:0]  sel,
    inout  wire [15:0] gpmc_d,
    output wire        gpmc_wait
);
    reg [2:0]  cs1_q=3'b111,oe_q=3'b111,we_q=3'b111;
    reg [6:0]  sel_q1=0,sel_q2=0; reg [15:0] d_q1=0,d_q2=0;
    reg [23:0] pr_q1=0, pr_q2=0;
    always @(posedge clk) begin
        cs1_q<={cs1_q[1:0],nCS1}; oe_q<={oe_q[1:0],nOE}; we_q<={we_q[1:0],nWE};
        sel_q1<=sel; sel_q2<=sel_q1; d_q1<=gpmc_d; d_q2<=d_q1;
        pr_q1<=probe; pr_q2<=pr_q1;
    end
    wire cs1_low=(cs1_q[2]==0); wire we_commit=(we_q[2]==0)&&(we_q[1]==1);
    wire [7:0] rd_sel={1'b0,sel[6:2],2'b00};
    reg [23:0] snap=0;
    reg [15:0] snap_data=0; reg [6:0] snap_addr=0;   // self-check: the KNOWN bus at snapshot
    always @(posedge clk) if (we_commit&&cs1_low) begin
        snap<=pr_q2; snap_data<=d_q2; snap_addr<=sel_q2;
    end
    wire [47:0] snappad = { 24'd0, snap };
    // keep the ADCs converting (harmless); drives are outputs, no bus conflict
    reg enc_clk=0; reg [15:0] dv=0;
    always @(posedge clk) begin if (dv>=3) begin dv<=0; enc_clk<=~enc_clk; end else dv<=dv+1; end
    assign adc_enc={8{enc_clk}}; assign adc_ctl_hi=4'b1111; assign adc_ctl_lo=3'b000;
    reg [15:0] rdata;
    always @* case(rd_sel)
        8'h10: rdata=16'hADC3;
        8'h38: rdata=snappad[15:0]; 8'h3C: rdata=snappad[31:16]; 8'h40: rdata=snappad[47:32];
        8'h44: rdata=snap_data; 8'h48: rdata={9'd0, snap_addr};
        default: rdata=0;
    endcase
    wire read_active=(~nCS1)&(~nOE);
    assign gpmc_d=read_active?rdata:16'hzzzz; assign gpmc_wait=1'b1;
endmodule
