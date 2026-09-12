#!/usr/bin/env python3
"""Audit capture build pins, timing, ADC input registers, and small recall RAM."""
import json,pathlib,re,subprocess,sys
out=pathlib.Path(sys.argv[1]).resolve()
subprocess.run([sys.executable,str(pathlib.Path(__file__).resolve().parents[1]/'sram_bench/audit.py'),str(out)],check=True)
fit=(out/'output_files/bench.fit.rpt').read_text(encoding='latin1')
for i in range(80):
 assert any('q_ioe[' in line and 'Fast Input Register assignment' in line and f'lane[{i}]~input' in line for line in fit.splitlines()),f'ADC lane {i} not packed in its input register'
summary=(out/'output_files/bench.fit.summary').read_text()
used=int(re.search(r'Total memory bits\s*:\s*([\d,]+)',summary)[1].replace(',',''))
interleave='`define INTERLEAVE' in (out/'bench.v').read_text()
expected=18944 if interleave else 16384
assert used==expected,f'Expected {expected} buffer bits, got {used}'
a=json.loads((out/'audit.json').read_text());a.update(adc_input_registers=80,on_chip_memory_bits=used,interleaved=interleave)
(out/'audit.json').write_text(json.dumps(a,indent=2)+'\n')
print('ADC IO input registers: 80/80; on-chip sample memory:',used,'bits')
