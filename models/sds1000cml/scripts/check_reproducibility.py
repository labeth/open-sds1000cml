#!/usr/bin/env python3
"""Compare maintained publication outputs before and after generation."""
import hashlib
import json
import subprocess
from inventory import ROOT


def snapshot():
    # Runtime lookup snapshots and this report are not generator outputs.
    return {str(p.relative_to(ROOT / 'generated')): hashlib.sha256(p.read_bytes()).hexdigest()
            for p in sorted((ROOT / 'generated').rglob('*')) if p.is_file()
            and p.name not in ('mcp-check.json', 'reproducibility.json')}


before = snapshot()
subprocess.run(['bash', str(ROOT / 'generate.sh')], check=True)
after = snapshot()
changed = sorted(k for k in set(before) | set(after) if before.get(k) != after.get(k))
report = dict(result='fail' if changed else 'pass', artifacts=len(after), changed=changed,
              excluded={'mcp-check.json': 'Independent MCP runtime snapshot; not maintained by generate.sh.'},
              sha256=after, scope='Consecutive local generations with unchanged inputs; absolute source metadata is retained. Cross-workspace reproducibility is not asserted.')
(ROOT / 'generated/reproducibility.json').write_text(json.dumps(report, indent=2) + '\n')
assert not changed, f'Generation changed artifacts: {changed}'
print(f'PASS: {len(after)} generator outputs are byte-identical')
