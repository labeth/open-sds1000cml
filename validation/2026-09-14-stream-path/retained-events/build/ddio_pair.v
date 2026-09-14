// ddio_pair.v -- ALTDDIO_OUT wrappers: one DDR output cell per ball (01-CONTRACT s2.1: the five
// encode pairs, K2, and the DDIO-class balls are all "REG_FEEDS_PIN_OUT" DDIO cells).
//
//   ddio_out1  one ball.  pad = outclock ? datain_h : datain_l (both registered on outclock).
//              {dh,dl} = {1,0} forwards outclock itself (a 200 MHz clock cannot pass through a
//              plain register to a pad at the same rate); {d,d} outputs an SDR bit; {0,0} static 0.
//   ddio_pair  two balls driven complementary from the same cell inputs: n gets {~dh,~dl}.
//
// SIM: `ifdef SIM selects a behavioural model with the same edge semantics as the Cyclone IV IOE
// (both inputs captured on the rising edge; the low half re-timed on the falling edge). Icarus
// cannot compile the Quartus altera_mf.v library at all (elaboration errors in unrelated modules),
// so the megafunction itself is only ever exercised by Quartus.

`timescale 1ns/1ps

module ddio_out1 (
    input  wire outclock,
    input  wire dh,
    input  wire dl,
    output wire pad
);
`ifdef SIM
    // one output register updated on both edges (no output mux -> no delta-cycle glitch)
    reg pad_r = 1'b0, l_r1 = 1'b0;
    always @(posedge outclock) begin pad_r <= dh; l_r1 <= dl; end
    always @(negedge outclock) pad_r <= l_r1;
    assign pad = pad_r;
`else
    altddio_out #(
        .extend_oe_disable      ("OFF"),
        .intended_device_family ("Cyclone IV E"),
        .invert_output          ("OFF"),
        .lpm_hint               ("UNUSED"),
        .lpm_type               ("altddio_out"),
        .oe_reg                 ("UNREGISTERED"),
        .power_up_high          ("OFF"),
        .width                  (1)
    ) u_ddio (
        .datain_h   (dh),
        .datain_l   (dl),
        .outclock   (outclock),
        .dataout    (pad),
        .aclr       (1'b0),
        .aset       (1'b0),
        .oe         (1'b1),
        .oe_out     (),
        .outclocken (1'b1),
        .sclr       (1'b0),
        .sset       (1'b0)
    );
`endif
endmodule

module ddio_pair (
    input  wire outclock,
    input  wire dh,
    input  wire dl,
    output wire pad_p,
    output wire pad_n
);
    ddio_out1 u_p (.outclock(outclock), .dh(dh),  .dl(dl),  .pad(pad_p));
    ddio_out1 u_n (.outclock(outclock), .dh(~dh), .dl(~dl), .pad(pad_n));
endmodule
