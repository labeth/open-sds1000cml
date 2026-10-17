// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
`timescale 1ns/1ps
// The fast image's serial receivers on the samples (ADR-SAMPLE-DOMAIN-SPI):
// the acquisition words (two samples a channel on the 250 MHz clock) paired
// for the 125 MHz receiver clock, one edge extractor on the first line (SPI's
// clock, the UART line, I2C's SCL) and one on the other (I2C's SDA), and the
// SPI, UART and I2C receivers, one enabled at a time. The second line's
// extractor reads the first at its crossings with the first's delay negated
// (the channels' sampling phase). Each line is sliced at its own level (the
// first line's, register 7; the other's, register 59), wherever it is read:
// the channels may sit at any offset and V/div (ADR-LINE-LEVELS).
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-229
module sample_bus(
 input wire core,clk,reset,
 input wire [31:0] word,input wire valid,  // core clock: CH1, CH2, CH1, CH2 bytes, the earliest lowest
 input wire [7:0] level,low,high,delay,    // the first line's level, band and delay, held (host clock)
 input wire [7:0] level2,low2,high2,       // the other line's level and band, held
 input wire [23:0] ticks,                  // SPI's idle gap in receiver clocks, UART's bit time in 1/16 ns
 input wire [31:0] pattern,
 input wire spi_en,spi_ch,spi_inverted,cpol,cpha,msb,input wire [2:0] spi_len,
 input wire uart_en,uart_ch,uart_inverted,uart_resync,input wire [3:0] uart_bits,input wire [1:0] uart_parity,input wire [2:0] uart_len,
 input wire i2c_en,i2c_ch,i2c_inverted,input wire [6:0] i2c_address,input wire i2c_any,input wire [1:0] i2c_direction,input wire [2:0] i2c_len,
 output wire spi_match,spi_event_valid,output wire [7:0] spi_event_kind,spi_event_data,
 output wire uart_match,uart_event_valid,output wire [7:0] uart_event_kind,output wire [8:0] uart_event_data,
 output wire i2c_match,i2c_event_valid,output wire [7:0] i2c_event_kind,i2c_event_data,output wire [1:0] i2c_event_meta,output wire i2c_selected
);
 // The receiver clock (in phase, half the rate) takes the two words since
 // its last edge.
 reg [31:0] w0=0,w1=0;reg v0=0,v1=0;
 always @(posedge core)begin w0<=word;w1<=w0;v0<=valid;v1<=v0;end
 // Held configuration, in the receiver clock.
 reg [7:0] delay_a=0,delay_b=0;
 reg ch_a=0,rise_a=1,fall_a=0,run_a=0,run_b=0;
 always @(posedge clk)begin
  delay_a<=delay;delay_b<=-delay;
  ch_a<=spi_en ? spi_ch : i2c_en ? i2c_ch : uart_ch;
  rise_a<=spi_en ? (cpol==cpha)!=spi_inverted : i2c_en ? !i2c_inverted : 1'b1;
  fall_a<=spi_en ? (cpol==cpha)==spi_inverted : i2c_en ? i2c_inverted : 1'b1;
  run_a<=spi_en || uart_en || i2c_en;run_b<=i2c_en;
 end
 wire live_a,found_a,rising_a,other_a;wire signed [7:0] when_a;wire [3:0] levels_a;
 sample_edges edges_a(.clk(clk),.reset(reset || !run_a),.valid(v0 && v1),.word({w0,w1}),.ch(ch_a),.rise_en(rise_a),.fall_en(fall_a),
  .level(level),.low(low),.high(high),.other_level(level2),.delay(delay_a),.live(live_a),.found(found_a),.when(when_a),.rising(rising_a),.other(other_a),.levels(levels_a));
 wire live_b,found_b,rising_b,other_b;wire signed [7:0] when_b;wire [3:0] levels_b;
 sample_edges edges_b(.clk(clk),.reset(reset || !run_b),.valid(v0 && v1),.word({w0,w1}),.ch(!ch_a),.rise_en(1'b1),.fall_en(1'b1),
  .level(level2),.low(low2),.high(high2),.other_level(level),.delay(delay_b),.live(live_b),.found(found_b),.when(when_b),.rising(rising_b),.other(other_b),.levels(levels_b));
 spi_sample spi(.clk(clk),.reset(reset),.enable(spi_en),.live(live_a),.found(found_a),.other(other_a),
  .inverted(spi_inverted),.msb(msb),.gap_ticks(ticks),.pattern(pattern),.pattern_len(spi_len),
  .match(spi_match),.event_valid(spi_event_valid),.event_kind(spi_event_kind),.event_data(spi_event_data));
 uart_sample uart(.clk(clk),.reset(reset),.enable(uart_en),.live(live_a),.found(found_a),.rising(rising_a),.when(when_a),.levels(levels_a),
  .inverted(uart_inverted),.resync(uart_resync),.period(ticks),.bits(uart_bits),.parity(uart_parity),.pattern(pattern),.pattern_len(uart_len),
  .match(uart_match),.event_valid(uart_event_valid),.event_kind(uart_event_kind),.event_data(uart_event_data));
 i2c_sample i2c(.clk(clk),.reset(reset),.enable(i2c_en),.live(live_a && live_b),.scl_found(found_a),.scl_other(other_a),.scl_when(when_a),
  .sda_found(found_b),.sda_rising(rising_b),.sda_other(other_b),.sda_when(when_b),.delay(delay_a),.inverted(i2c_inverted),
  .address(i2c_address),.any_address(i2c_any),.direction(i2c_direction),.pattern(pattern),.pattern_len(i2c_len),
  .match(i2c_match),.event_valid(i2c_event_valid),.event_kind(i2c_event_kind),.event_data(i2c_event_data),.event_meta(i2c_event_meta),.selected(i2c_selected));
endmodule
