#!/usr/bin/env python3
"""Triage every PDF page; a passing geometry check never substitutes for visual review."""
import hashlib
import json
import logging
import subprocess
from pathlib import Path
import pdfplumber
ROOT = Path(__file__).resolve().parents[1]


def audit():
    target = ROOT / 'output/pdf/architecture.draft.pdf'
    build = json.loads((ROOT / 'evidence/pdf-build.json').read_text())
    for path, sha in build['sha256'].items():
        assert hashlib.sha256((ROOT / path).read_bytes()).hexdigest() == sha, f'stale PDF build: {path}'
    # Poppler provides navigation text without pdfplumber's slower layout grouping.
    text_pages = subprocess.check_output(['pdftotext', '-layout', str(target), '-'], text=True).split('\f')
    logging.getLogger('pdfminer').setLevel(logging.ERROR)
    rows = []
    with pdfplumber.open(target) as pdf:
        for number, page in enumerate(pdf.pages, 1):
            chars = [c for c in page.chars if c['text'].strip()]
            outside = [c for c in chars if c['x0'] < -1 or c['x1'] > page.width+1 or c['top'] < -1 or c['bottom'] > page.height+1]
            tiny = [c for c in chars if c['size'] < 6]
            text = text_pages[number-1]
            rows.append(dict(page=number, chars=len(chars), outside=len(outside), tinyChars=len(tiny),
                             minFont=min([c['size'] for c in chars] or [0]),
                             hostView='VIEW-HOST-RTL' in text, hardwareView='VIEW-HARDWARE' in text,
                             decomposition='System Decomposition Diagram' in text,
                             publicationRequirement='REQ-SDS-094' in text))
            page.close()
            if number % 50 == 0:
                print(f'Inspected geometry on {number} PDF pages', flush=True)
    report = dict(result='needs-work' if any(r['outside'] or r['tinyChars'] for r in rows) else 'awaiting-visual-review',
                  pdf='output/pdf/architecture.draft.pdf', input='generated/ARCHITECTURE.adoc',
                  pdfSHA256=build['sha256']['output/pdf/architecture.draft.pdf'],
                  inputSHA256=build['sha256']['generated/ARCHITECTURE.adoc'], pages=len(rows),
                  method=f'pdfplumber {pdfplumber.__version__} character bounds/fonts and Poppler navigation text',
                  scope='Automated page bounds and sub-six-point text triage only. No physical or model qualification; visual review remains separate.',
                  rows=rows, visualReview=[])
    (ROOT / 'evidence/pdf-review.json').write_text(json.dumps(report, indent=2)+'\n')
    print(report['result'], 'pages=', len(rows), 'outside=', sum(r['outside'] for r in rows),
          'tiny-text pages=', [r['page'] for r in rows if r['tinyChars']], flush=True)
    return report


if __name__ == '__main__':
    audit()
