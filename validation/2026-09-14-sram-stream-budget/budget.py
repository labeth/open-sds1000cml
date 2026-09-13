#!/usr/bin/env python3
"""Necessary bandwidth bounds, not a simulation or hardware qualification."""
import math
CLOCK = 250_000_000
DEPTH = 524288
RATE = 500_000_000 / 256
# One complete forward address circuit for read/return, followed by a write
# catch-up. Assume one useful read or write each clock and no turnaround cost.
for batch in (4096, 4608, 5120, 8192):
    ceiling = CLOCK * batch / (DEPTH + batch)
    print(f"{batch} words/read: ideal ceiling {ceiling:.3f} words/s; "
          f"D8 {'possible' if ceiling > RATE else 'IMPOSSIBLE'}")
minimum = math.floor(RATE * DEPTH / (CLOCK - RATE)) + 1
assert minimum == 4129
assert CLOCK * 4096 / (DEPTH + 4096) < RATE
assert CLOCK * 5120 / (DEPTH + 5120) > RATE
print(f"Minimum ideal batch: {minimum} words; ignores turnaround and host stalls")
print(f"Arrivals during one counter circuit: {RATE * DEPTH / CLOCK:.0f} words")
print(f"SRAM cover at D8: {DEPTH / RATE * 1000:.3f} ms before safety reserve")
