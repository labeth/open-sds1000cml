// 100 end-to-end operator workflows. Each mimics a real task: an FPGA source
// drives the scope, the operator drives the GUI / hard buttons, and reads the
// RESULT off the screen. `source` names the FPGA config that must be flashed
// (see e2e/README for the flash command per source).
//
// Assertions are on GUI-READ values (measurement panel, decode text, eye/jitter
// table, zone meter) — the numbers a human would trust — never on API data.


import { tone1M, tone1Mb, prbs2M, prbs2Mb } from "./workflow_analog.mjs";
import { uart, spi, spiB, burst } from "./workflow_serial.mjs";
import { maskv } from "./workflow_mask.mjs";

export const WORKFLOWS = [
  ...spi.map((w) => ({ ...w, source: "spi" })),
  ...spiB.map((w) => ({ ...w, source: "spi" })),
  ...burst.map((w) => ({ ...w, source: "burst" })),
  ...uart.map((w) => ({ ...w, source: "uart" })),
  ...tone1M.map((w) => ({ ...w, source: "tone1M" })),
  ...tone1Mb.map((w) => ({ ...w, source: "tone1M" })),
  ...prbs2M.map((w) => ({ ...w, source: "prbs2M" })),
  ...prbs2Mb.map((w) => ({ ...w, source: "prbs2M" })),
  ...maskv.map((w) => ({ ...w, source: "maskviol" })),
];

