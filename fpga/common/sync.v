// sync.v -- clock-domain crossing primitives shared by every acq2 design.
//
//   sync_bit   #(W)   plain 2-flop synchronizer for level signals (one per bit; the bits of a
//                     vector may skew by a cycle -- use only for quasi-static words or single bits).
//   sync_pulse        1-cycle pulse from domain A to a 1-cycle pulse in domain B (toggle + edge).
//                     Pulses closer than ~4 B-cycles are merged; the caller must not do that.
//   sync_word  #(W)   continuous handshake transfer of a W-bit word: every value presented on
//                     q_dst was a coherent sample of d_src (never a bit-mix). Latency ~6-8 cycles.
//                     Works for any counter width (non-power-of-two wraps included), unlike gray.
//   stretch3          3-flop input synchronizer with level + rise/fall outputs (async pad inputs).
//
// All flops carry power-up initial values (Cyclone IV honours them); there is no reset net.
// Idea lineage: owned-fpga il_capture.v used toggle synchronizers for arm/done (03-PRIOR-ART);
// the handshake word transfer is new here.

`timescale 1ns/1ps

module sync_bit #(
    parameter integer W = 1
)(
    input  wire         clk,
    input  wire [W-1:0] d,
    output reg  [W-1:0] q
);
    reg [W-1:0] meta = {W{1'b0}};
    initial q = {W{1'b0}};
    always @(posedge clk) begin
        meta <= d;
        q    <= meta;
    end
endmodule

module sync_pulse (
    input  wire clk_src,
    input  wire pulse_src,
    input  wire clk_dst,
    output wire pulse_dst
);
    reg       tgl = 1'b0;
    reg [2:0] s   = 3'b000;
    always @(posedge clk_src) if (pulse_src) tgl <= ~tgl;
    always @(posedge clk_dst) s <= {s[1:0], tgl};
    assign pulse_dst = s[2] ^ s[1];
endmodule

module sync_word #(
    parameter integer W = 16
)(
    input  wire         clk_src,
    input  wire [W-1:0] d_src,
    input  wire         clk_dst,
    output reg  [W-1:0] q_dst
);
    reg         req     = 1'b0;     // dst-domain toggle: "send me a sample"
    reg         ack     = 1'b0;     // src-domain toggle: "hold is valid"
    reg [W-1:0] hold    = {W{1'b0}};
    reg [2:0]   req_s   = 3'b000;
    reg [2:0]   ack_s   = 3'b000;
    reg         started = 1'b0;
    initial q_dst = {W{1'b0}};

    always @(posedge clk_src) begin
        req_s <= {req_s[1:0], req};
        if (req_s[2] ^ req_s[1]) begin
            hold <= d_src;
            ack  <= ~ack;
        end
    end

    always @(posedge clk_dst) begin
        ack_s <= {ack_s[1:0], ack};
        if (!started) begin
            started <= 1'b1;
            req     <= ~req;
        end else if (ack_s[2] ^ ack_s[1]) begin
            q_dst <= hold;      // hold is stable: src rewrites it only after our next req
            req   <= ~req;
        end
    end
endmodule

module stretch3 (
    input  wire clk,
    input  wire d,
    output wire level,      // settled level (stage 2)
    output wire rise,       // 1-cycle pulse on a 0->1 seen between stage 2 and stage 1
    output wire fall
);
    reg [2:0] q = 3'b000;
    always @(posedge clk) q <= {q[1:0], d};
    assign level = q[2];
    assign rise  = ~q[2] &  q[1];
    assign fall  =  q[2] & ~q[1];
endmodule
