// lane_in.v -- registered input lanes and the lane diagnostics shared by the acq2 designs.
//
//   lane_in    #(N)  the pad register: N inputs registered on `clk` with the fast-input-register
//                    attribute so the flop sits in the IOE (05-WORKPLAN s3: register at the pad,
//                    no logic between the pad register and the sample path).
//   lane_mon   #(N)  per-signal ever-1 / ever-0 flags, all cleared while gate_rst is high
//                    (DIAG LANE_LVL[2:1]); every monitored signal is tracked concurrently so a
//                    lane census after one gate reset needs no per-lane re-arm.
//   toggle_ctr       ONE windowed toggle counter: counts observed transitions of `d` during a
//                    65536-cycle window, saturating at 0xFFFF, latches at the window end and
//                    restarts (DIAG LANE_TOG). The design muxes the selected LANE_IDX signal into
//                    it; a reading is valid two windows (2 x 65536 clk cycles) after LANE_IDX
//                    changes. One counter instead of 116 keeps the diagnostics under ~300 LEs.

`timescale 1ns/1ps

module lane_in #(
    parameter integer N = 80
)(
    input  wire         clk,
    input  wire [N-1:0] pad,
    output reg  [N-1:0] q
);
    initial q = {N{1'b0}};
    (* useioff = 1 *) reg [N-1:0] q_ioe = {N{1'b0}};
    always @(posedge clk) begin
        q_ioe <= pad;
        q     <= q_ioe;      // second stage: keeps the IOE flop free of fan-out constraints
    end
endmodule

module lane_mon #(
    parameter integer N = 128
)(
    input  wire         clk,
    input  wire [N-1:0] d,
    input  wire         gate_rst,
    output reg  [N-1:0] ever1,
    output reg  [N-1:0] ever0
);
    initial begin ever1 = {N{1'b0}}; ever0 = {N{1'b0}}; end
    always @(posedge clk) begin
        if (gate_rst) begin
            ever1 <= {N{1'b0}};
            ever0 <= {N{1'b0}};
        end else begin
            ever1 <= ever1 |  d;
            ever0 <= ever0 | ~d;
        end
    end
endmodule

module toggle_ctr (
    input  wire        clk,
    input  wire        d,
    output reg  [15:0] count,       // last completed window
    output reg         window_done  // 1-cycle pulse at each window end
);
    reg [15:0] win  = 16'd0;
    reg [15:0] acc  = 16'd0;
    reg        d_q  = 1'b0;
    initial begin count = 16'd0; window_done = 1'b0; end
    wire tog = d ^ d_q;
    always @(posedge clk) begin
        d_q         <= d;
        win         <= win + 16'd1;
        window_done <= 1'b0;
        if (win == 16'hffff) begin
            count       <= (tog && acc != 16'hffff) ? (acc + 16'd1) : acc;
            acc         <= 16'd0;
            window_done <= 1'b1;
        end else if (tog && acc != 16'hffff) begin
            acc <= acc + 16'd1;
        end
    end
endmodule
