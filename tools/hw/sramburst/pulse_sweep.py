#!/usr/bin/env python3
"""Same-cold-boot pulse series; vendor must already be SIGSTOPped.
Only invokes the bounded CS1 probe in RAM and read-only MAX V SAMPLE.
"""
import datetime, json, pathlib, subprocess, sys
ROOT = pathlib.Path(__file__).resolve().parents[3]
sys.path.insert(0, "/home/labeth/ws/maxv")
from maxv_bsdl import triads
TRI, _ = triads()
PINS = [15,16,17,18,19,20,69,78,81,82,83,84,85,86,89,91,92,95,96]
OUT = ROOT / "the acq2 analysis branch" / ("2026-09-11-maxv-pulses-" + datetime.datetime.now(datetime.timezone.utc).strftime("%H%M%S"))
OUT.mkdir(exist_ok=False)
print("Output:", OUT, flush=True)
CTL = [str(ROOT / "ota/dist/otactl"), "-tcp", "192.168.1.209:5900", "exec"]
def run(args):
    p = subprocess.run(args, capture_output=True, text=True, timeout=15)
    if p.returncode: raise RuntimeError(p.stdout + p.stderr)
    return p.stdout + p.stderr
state = run(CTL + ["cat", "/proc/534/status"])
if "T (stopped)" not in state: raise RuntimeError("vendor is not suspended")
(OUT / "vendor-state.txt").write_text(state)
rows = []
for i, us in enumerate(map(int, sys.argv[1:])):
    label = f"{i:03d}-{us}us"
    raw = run(CTL + ["/dev/sramburst", f"--pulse-us={us}"])
    (OUT / (label + ".json.txt")).write_text(raw)
    d = json.JSONDecoder().raw_decode(raw)[0]
    if not all("ok=true" in s for s in d["sequence"]): raise RuntimeError("write failed")
    raw = run(["openocd", "-f", str(ROOT / "the acq2 analysis branch")])
    (OUT / (label + ".jtag.txt")).write_text(raw)
    caps = [int(s[4:],16) for s in raw.splitlines() if s.startswith("CAP=")]
    if len(caps) != 3 or "0x020a50dd" not in raw: raise RuntimeError("invalid TAP/capture")
    bits = []
    for pin in PINS:
        if any((c >> TRI[pin]["ctl"]) & 1 for c in caps): raise RuntimeError("direction changed")
        values = {(c >> TRI[pin]["out"]) & 1 for c in caps}
        if len(values) != 1: raise RuntimeError("counter not static")
        bits.append(values.pop())
    row = {"index": i, "requested_us": us, "elapsed_ns": d["run_to_halt_ns"], "pins": PINS, "bits": bits}
    rows.append(row)
    (OUT / "rows.json").write_text(json.dumps(rows, indent=2) + "\n")
    print(us, d["run_to_halt_ns"], "".join(map(str,bits)), flush=True)
