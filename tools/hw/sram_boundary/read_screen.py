#!/usr/bin/env python3
"""Read-only DQ screen under existing EXTEST; never enables a DQ output.

Vary six factory-output control candidates, pulse one candidate clock, and
look for external LOW bits. A hit is only a responder, not yet SRAM proof.
"""
import json
from boundary import Tap, OUT, CY, MV, DQ, GP, ADDR, setbit, dq_word

s = json.loads((OUT / "state.json").read_text())
cy, mv = Tap("cyc", 16667, 603), Tap("maxv", 16666, 240)
cv, vv = int(s["cy_vector"], 16), int(s["mv_vector"], 16)
controls = ["F1", "G1", "K1", "G2", "D1", "A11"]
clocks = [("cyc", p) for p in ["K2", "F2", "J2"]] + [("maxv", p) for p in [100,97,88,37,33,8,7,6,5,4,3]]
assert all((cv >> CY[b]["control_cell"]) & 1 == 1 for b in DQ + GP)
assert all((cv >> CY[b]["control_cell"]) & 1 == 0 for b in controls + ["K2", "F2", "J2"])
for p in ADDR:
    vv = setbit(vv, MV[p]["out"], 0)
rows = []
try:
    for mask in range(64):
        cc = cv
        for i, p in enumerate(controls):
            cc = setbit(cc, CY[p]["output_cell"], (mask >> i) & 1)
        cy.dr(cc)
        for chip, pin in clocks:
            mv.dr(vv)
            if chip == "cyc":
                for _ in range(4):
                    for level in (0, 1):
                        cc = setbit(cc, CY[pin]["output_cell"], level)
                        cy.dr(cc)
            else:
                mm = vv
                for _ in range(4):
                    for level in (0, 1):
                        mm = setbit(mm, MV[pin]["out"], level)
                        mv.dr(mm)
            values = [dq_word(cy.dr(cc)) for _ in range(3)]
            row = {"mask": mask, "controls": controls, "clock": [chip, pin], "dq": list(map(hex, values))}
            rows.append(row)
            if any(v != 0xffffffff for v in values):
                print("RESPONDER", row, flush=True)
                (OUT / "read-screen-hit.json").write_text(json.dumps(row, indent=2) + "\n")
                raise SystemExit(0)
        if mask % 8 == 7:
            print("completed", len(rows), "conditions", flush=True)
finally:
    mv.dr(int(s["mv_vector"], 16))
    cy.dr(cv)
    (OUT / "read-screen.json").write_text(json.dumps(rows, indent=2) + "\n")
    print("Restored EXTEST baseline vectors; data bus stays released", flush=True)
