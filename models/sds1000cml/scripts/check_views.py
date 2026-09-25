#!/usr/bin/env python3
"""Check generated SVG text bounds using conservative fixed-pitch estimates."""
import json
import xml.etree.ElementTree as ET
import yaml
from inventory import ROOT


def check():
    views = yaml.safe_load((ROOT / 'model/views.yml').read_text())['views']
    expected = {v['id'] for v in views}
    files = sorted((ROOT / 'generated/views').glob('*.svg'))
    assert {f.stem for f in files} == expected, 'SVG views differ from authored views'
    reports = []
    for path in files:
        svg = ET.parse(path).getroot()
        _, _, width, height = map(float, svg.attrib['viewBox'].split())
        violations = []
        texts = svg.findall('.//{*}text')
        for text in texts:
            x, y = float(text.attrib['x']), float(text.attrib['y'])
            size = float(text.attrib['font-size'])
            value = ''.join(text.itertext())
            if x < 0 or y - size < 0 or y + size * .25 > height or x + len(value) * size * .65 > width:
                violations.append(value)
        reports.append(dict(view=path.stem, width=width, height=height,
                            textLabels=len(texts), estimatedTextBoundsViolations=violations))
    failed = any(r['estimatedTextBoundsViolations'] for r in reports)
    report = dict(result='fail' if failed else 'pass', views=reports,
                  scope='XML parse and conservative monospace text-bound estimates; not semantic completeness or a substitute for image review.')
    (ROOT / 'generated/diagram-layout-check.json').write_text(json.dumps(report, indent=2) + '\n')
    assert not failed, 'SVG text exceeds canvas bounds'
    print(f'PASS: {len(files)} SVG views parse and text estimates fit their canvas')


if __name__ == '__main__':
    check()
