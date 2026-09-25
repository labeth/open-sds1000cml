#!/usr/bin/env python3
"""Check committed coefficients against the generator's grid without rewriting source."""
import hashlib
import json
import math
import re
from inventory import ROOT, REV, git


def run():
    paths = ['app/internal/dsp/precision_taps.go', 'tools/dsp/precision_filter.py', 'specs/precision-filter-response.json']
    sources = {p: git('show', f'{REV}:{p}') for p in paths}
    values = sources[paths[0]].decode().split('int32{', 1)[1].split('}', 1)[0]
    taps = [int(x) for x in re.findall(r'-?\d+', values)]
    assert len(taps) == 63 and taps == taps[::-1] and sum(taps) == 1 << 22
    frequencies = [i / 4096 for i in range(2049)]
    response = [abs(sum(c / (1 << 22) * math.cos(2*math.pi*f*(i-31)) for i,c in enumerate(taps))) for f in frequencies]
    stop = 20*math.log10(max(h for f,h in zip(frequencies,response) if f >= .25))
    def sinc(x):
        return math.sin(math.pi*x)/(math.pi*x) if x else 1.0
    expected = json.loads(sources[paths[2]])
    assert [x['log2'] for x in expected] == list(range(4,21))
    checks=[]
    for row in expected:
        reduction=1 << row['log2']
        ripple=max(abs(20*math.log10(h*(sinc(f)/sinc(f/reduction))**3)) for f,h in zip(frequencies,response) if f <= .075)
        assert ripple < .005 and stop < -90
        assert row['passband_fraction']==.075 and row['stopband_from_fraction']==.25
        assert abs(ripple-row['max_passband_error_db']) < 1e-6
        assert abs(stop-row['stopband_max_db']) < 1e-6
        checks.append(dict(log2=row['log2'],maxPassbandErrorDB=ripple,stopbandMaxDB=stop))
    report=dict(result='pass',commit=REV,sourceSHA256={p:hashlib.sha256(b).hexdigest() for p,b in sources.items()},tapCount=len(taps),symmetric=True,coefficientSum=sum(taps),gridPoints=len(frequencies),checks=checks,
        scope='Independent scalar evaluation of the committed quantized coefficients on the generator 2049-point frequency grid. Matches recorded response within 1e-6 dB. This is sampled digital response, not continuous-frequency proof, ARM execution, coefficient regeneration or physical qualification.')
    (ROOT/'evidence/precision-response-check.json').write_text(json.dumps(report,indent=2)+'\n')
    print('PASS: symmetric 63-tap Q22 coefficients and 17 sampled response checks')


if __name__ == '__main__':
    run()
