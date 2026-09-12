#!/usr/bin/env python3
"""Volatile boundary-scan SRAM experiment; never uses configuration/ISP opcodes.

Start both localhost OpenOCD servers, cold boot, STOP acquisition, suspend vendor.
`enter` captures safe directions, releases Cyclone DQ, then freezes MAX V outputs.
`restore` returns both TAPs to BYPASS. State and raw command replies are local files.
"""
import json
import pathlib
import socket
import sys
import time

ROOT = pathlib.Path(__file__).resolve().parents[3]
OUT = ROOT / "the acq2 analysis branch"
OUT.mkdir(exist_ok=True)
sys.path.insert(0, "/home/labeth/ws/maxv")
from maxv_bsdl import triads

MV, _ = triads()
CY = json.loads(pathlib.Path("/home/labeth/ws/maxv/cyclone/ball_cell_map.json").read_text())["balls"]
DQ = "J6 F5 L2 L1 L3 N2 N1 K5 L4 R1 P2 P1 F3 G5 N3 P3 N5 N6 D3 M6 R5 T5 R6 T6 R3 R7 T7 T3 T2 R4 T4 F7".split()
GP = "A10 B9 A9 B8 A8 B7 A7 B6 A6 D6 C6 B5 A5 F6 D5 E5".split()
ADDR = [20, 19, 18, 17, 16, 15, 86, 85, 84, 83, 82, 81, 78, 96, 95, 92, 91, 89, 69]


class Tap:
    def __init__(self, name, port, bits):
        self.name, self.bits = name, bits
        self.sock = socket.create_connection(("127.0.0.1", port), timeout=5)

    def cmd(self, command):
        self.sock.sendall(command.encode() + b"\x1a")
        result = b""
        while not result.endswith(b"\x1a"):
            chunk = self.sock.recv(65536)
            if not chunk:
                raise RuntimeError("OpenOCD connection closed")
            result += chunk
        text = result[:-1].decode()
        with (OUT / "commands.jsonl").open("a") as f:
            f.write(json.dumps({"time_ns": time.time_ns(), "tap": self.name,
                                "command": command, "reply": text}) + "\n")
        if any(word in text.lower() for word in ("error", "invalid", "failed")):
            raise RuntimeError(text)
        return text

    def ir(self, value):
        assert value in (5, 15, 1023), "Only SAMPLE, EXTEST and BYPASS are allowed"
        self.cmd(f"irscan {self.name}.tap 0x{value:x}")

    def dr(self, value):
        assert 0 <= value < 1 << self.bits
        text = self.cmd(f"drscan {self.name}.tap {self.bits} 0x{value:x}").strip()
        return int(text, 16)

    def sample(self):
        self.ir(5)
        return [self.dr((1 << self.bits) - 1) for _ in range(3)]


def setbit(v, bit, value):
    return (v & ~(1 << bit)) | (int(value) << bit)


def cy_vector(cap):
    value = (1 << 603) - 1
    for ball, cells in CY.items():
        ctl, output = cells.get("control_cell"), cells.get("output_cell")
        if ctl is None or output is None:
            continue
        # Never enable an original input; DQ and all GPMC data are released.
        released = ((cap >> ctl) & 1) or ball in DQ or ball in GP
        value = setbit(value, ctl, released)
        value = setbit(value, output, (cap >> output) & 1)
    return value


def mv_vector(cap):
    value = (1 << 240) - 1
    for cells in MV.values():
        value = setbit(value, cells["ctl"], (cap >> cells["ctl"]) & 1)
        value = setbit(value, cells["out"], (cap >> cells["out"]) & 1)
    return value


def dq_word(cap):
    return sum(((cap >> CY[ball]["input_cell"]) & 1) << i for i, ball in enumerate(DQ))


def main():
    mode = sys.argv[1]
    cy, mv = Tap("cyc", 16667, 603), Tap("maxv", 16666, 240)
    if mode == "restore":
        mv.ir(1023)
        cy.ir(1023)
        print("Both TAPs restored to BYPASS")
        return
    if mode != "enter":
        raise ValueError("use enter or restore")
    if (OUT / "state.json").exists():
        raise RuntimeError("state.json exists; do not overwrite a trial")
    assert "0x020f10dd" in cy.cmd("scan_chain").lower()
    assert "0x020a50dd" in mv.cmd("scan_chain").lower()
    cc, mc = cy.sample(), mv.sample()
    for b in DQ:
        assert all((x >> CY[b]["control_cell"]) & 1 == 0 for x in cc)
    for b in GP:
        assert all((x >> CY[b]["control_cell"]) & 1 == 1 for x in cc)
    driven = [p for p, t in MV.items() if all((x >> t["ctl"]) & 1 == 0 for x in mc)]
    assert len(driven) == 39
    for pin in (61, 66):
        assert all((x >> MV[pin]["out"]) & 1 == 1 for x in mc)
    cv, mvv = cy_vector(cc[-1]), mv_vector(mc[-1])
    state = {"cy_caps": list(map(hex, cc)), "mv_caps": list(map(hex, mc)),
             "cy_vector": hex(cv), "mv_vector": hex(mvv), "mv_driven": driven,
             "dq_bit_to_ball": DQ, "counter_bit_to_pin": ADDR}
    (OUT / "state.json").write_text(json.dumps(state, indent=2) + "\n")
    # Preload under SAMPLE, then select EXTEST. Clock/data pins change only here.
    cy.dr(cv)
    cy.ir(15)
    ccheck = cy.dr(cv)
    # Capture cells expose core-side values even in EXTEST: the D2 test changes
    # its physical input but leaves its captured output at the core's zero.
    # Validate the shifted disable vector itself, not those core capture cells.
    assert all((cv >> CY[b]["control_cell"]) & 1 == 1 for b in DQ + GP)
    assert ((cy.dr(cv) >> CY["D2"]["input_cell"]) & 1) == ((cv >> CY["D2"]["output_cell"]) & 1)
    mv.dr(mvv)
    mv.ir(15)
    mv.dr(mvv)
    words = [dq_word(cy.dr(cv)) for _ in range(10)]
    state["released_dq_words"] = list(map(hex, words))
    (OUT / "state.json").write_text(json.dumps(state, indent=2) + "\n")
    print("DQ/GPMC disable vector checked; D2 physical output checked; MAX V original directions retained")
    print("DQ words:", list(map(hex, words)))
    print("EXTEST remains active until restore")


if __name__ == "__main__":
    main()
