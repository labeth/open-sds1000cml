// tb_eth_gearbox_dir.v -- directed gearbox check: value/order + partial flush tail.
// Writes 27 known samples (values 100..126) as WR_SAMP=3 triplets (9 triplets),
// checks the gearbox emits three full 8-lane words {100..107},{108..115},
// {116..123} then, on flush, a partial 3-lane word {124,125,126} -- correct
// order (lane0=earliest), no loss, no overflow.
`timescale 1ns/1ps
module tb_eth_gearbox_dir;
    localparam integer SAMPLE_W = 12, LANES = 8, WR_SAMP = 3;
    integer i, j, errs;
    reg wr_clk, rd_clk, wr_rst, rd_rst, en, wr_valid, flush, rd_ready;
    reg  [WR_SAMP*SAMPLE_W-1:0] wr_samp;
    wire [LANES*SAMPLE_W-1:0]   codes;
    wire [3:0]                  nvalid;
    wire                        in_valid, gb_ovf;

    integer NS; reg [SAMPLE_W-1:0] sm [0:31];
    integer widx;

    eth_gearbox #(.SAMPLE_W(SAMPLE_W), .LANES(LANES), .WR_SAMP(WR_SAMP), .DEPTHW(4)) gb (
        .wr_clk(wr_clk), .wr_rst(wr_rst), .wr_valid(wr_valid), .wr_samp(wr_samp),
        .rd_clk(rd_clk), .rd_rst(rd_rst), .rd_ready(rd_ready), .flush(flush),
        .en(en), .codes(codes), .nvalid(nvalid), .in_valid(in_valid), .overflow(gb_ovf));

    always #2.5  wr_clk = ~wr_clk;
    always #6.25 rd_clk = ~rd_clk;

    // capture emitted words
    integer wcount; reg [3:0] wnv [0:7]; reg [SAMPLE_W-1:0] wlane [0:7][0:7];
    always @(posedge rd_clk) begin
        if (in_valid && !rd_rst && en && wcount < 8) begin
            wnv[wcount] = nvalid;
            for (j = 0; j < LANES; j = j + 1) wlane[wcount][j] = codes[j*SAMPLE_W +: SAMPLE_W];
            wcount = wcount + 1;
        end
    end

    initial begin
        wr_clk=0; rd_clk=0; wr_rst=1; rd_rst=1; en=0; wr_valid=0; flush=0; rd_ready=1;
        wr_samp=0; wcount=0; errs=0;
        NS = 27;
        for (i = 0; i < NS; i = i + 1) sm[i] = 100 + i;
        en = 1;
        repeat (4) @(posedge wr_clk);
        @(negedge wr_clk) wr_rst=0; @(negedge rd_clk) rd_rst=0;

        // feed 3 full triplets (9 samples 100..108) then 1 more triplet is not
        // available (NS=11 -> 9 via 3 triplets, then 2 leftover need a 4th... to
        // keep full triplets we write 3 triplets=9, then 1 triplet with 108,109,110)
        widx = 0;
        while (widx + WR_SAMP <= NS) begin
            @(negedge wr_clk);
            for (j = 0; j < WR_SAMP; j = j + 1) wr_samp[j*SAMPLE_W +: SAMPLE_W] = sm[widx+j];
            wr_valid = 1;
            widx = widx + WR_SAMP;
        end
        @(negedge wr_clk) wr_valid = 0;

        repeat (20) @(negedge rd_clk);
        @(negedge rd_clk) flush = 1;
        repeat (3) @(negedge rd_clk);
        @(negedge rd_clk) flush = 0;
        repeat (6) @(negedge rd_clk);

        // expect exactly 4 words: three full then partial {124,125,126}
        if (gb_ovf) begin $display("FAIL[dir]: overflow"); errs=errs+1; end
        if (wcount != 4) begin $display("FAIL[dir]: word count=%0d exp 4", wcount); errs=errs+1; end
        for (i = 0; i < 3 && i < wcount; i = i + 1) begin
            if (wnv[i] != 8) begin $display("FAIL[dir]: word%0d nvalid=%0d exp 8", i, wnv[i]); errs=errs+1; end
            for (j = 0; j < 8; j = j + 1)
                if (wlane[i][j] != 100 + 8*i + j) begin
                    $display("FAIL[dir]: word%0d lane%0d=%0d exp %0d", i, j, wlane[i][j], 100+8*i+j); errs=errs+1; end
        end
        if (wcount >= 4) begin
            if (wnv[3] != 3) begin $display("FAIL[dir]: word3 nvalid=%0d exp 3", wnv[3]); errs=errs+1; end
            for (j = 0; j < 3; j = j + 1)
                if (wlane[3][j] != 124+j) begin
                    $display("FAIL[dir]: word3 lane%0d=%0d exp %0d", j, wlane[3][j], 124+j); errs=errs+1; end
        end
        if (errs == 0)
            $display("PASS[dir]: 3 full words {100..123} + flush partial {124,125,126}, order/count exact, overflow=0");
        $finish;
    end
    initial begin #500000; $display("FAIL[dir]: timeout"); $finish; end
endmodule
