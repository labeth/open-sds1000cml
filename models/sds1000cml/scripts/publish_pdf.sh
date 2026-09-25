#!/usr/bin/env bash
set -euo pipefail
model_dir=$(cd -- "$(dirname -- "$0")/.." && pwd)
mkdir -p "$model_dir/output/pdf"
pdf_input_sha=$(python3 -c 'import hashlib,sys;print(hashlib.sha256(open(sys.argv[1],"rb").read()).hexdigest())' "$model_dir/generated/ARCHITECTURE.adoc")
proven-docs render "$model_dir/generated/ARCHITECTURE.adoc" \
  --output "$model_dir/output/pdf/architecture.draft.pdf" \
  > "$model_dir/evidence/pdf-render.log" 2>&1
python3 - "$model_dir" "$pdf_input_sha" <<'PY'
import hashlib,json,sys
from pathlib import Path
root=Path(sys.argv[1])
assert hashlib.sha256((root/'generated/ARCHITECTURE.adoc').read_bytes()).hexdigest()==sys.argv[2], 'publication input changed during rendering'
paths=['generated/ARCHITECTURE.adoc','output/pdf/architecture.draft.pdf']
report=dict(result='rendered-awaiting-review',command='bash models/sds1000cml/scripts/publish_pdf.sh',
            sha256={p:hashlib.sha256((root/p).read_bytes()).hexdigest() for p in paths},
            scope='PDF export only; page bounds, font sizes and visual layout still require review. PDF byte reproducibility is not asserted.')
(root/'evidence/pdf-build.json').write_text(json.dumps(report,indent=2)+'\n')
print('Draft PDF rendered; layout review required')
PY
