#!/usr/bin/env python3
"""List C/shell declaration candidates from unchanged pinned sources, without execution."""
import hashlib
import json
import subprocess
from inventory import ROOT, SOURCE, REV, git


def audit():
    inventory = json.loads((ROOT/'evidence/source-inventory.json').read_text())
    files = []
    for item in inventory:
        path = item['path']
        if item['category'] == 'historical-validation':
            continue
        language = 'C' if path.endswith(('.c', '.h')) else 'Sh' if path.endswith('.sh') else None
        if language is None:
            continue
        data = git('show', f'{REV}:{path}')
        source = SOURCE/path
        assert source.read_bytes() == data, 'unreviewed source change: '+path
        command = ['ctags', '--options=NONE', '--output-format=json', '--fields=+n',
                   '--kinds-'+language+'=f', '--language-force='+language, '-f', '-', str(source)]
        process = subprocess.run(command, text=True, capture_output=True, check=True)
        rows = []
        for line in process.stdout.splitlines():
            tag = json.loads(line)
            assert tag['_type'] == 'tag' and tag['kind'] == 'function', tag
            assert tag['path'] == str(source) and 0 < tag['line'] <= len(data.splitlines()), tag
            rows.append(dict(name=tag['name'], line=tag['line'], pattern=tag['pattern']))
        files.append(dict(path=path, language=language, owner=item['owner'], gitBlob=item['gitBlob'],
                          sourceSHA256=hashlib.sha256(data).hexdigest(),
                          candidates=sorted(rows, key=lambda r: (r['line'], r['name']))))
    counts = {lang: dict(files=sum(f['language']==lang for f in files),
                        functionCandidates=sum(len(f['candidates']) for f in files if f['language']==lang))
              for lang in ['C', 'Sh']}
    report = dict(status='candidate-inventory-only', commit=REV, counts=counts, files=files,
                  extractor=subprocess.check_output(['ctags', '--version'], text=True).splitlines()[0],
                  options=['--options=NONE', '--output-format=json', '--fields=+n', '--kinds-LANGUAGE=f', '--language-force=LANGUAGE'],
                  scope='All nonhistorical .c/.h/.sh files in the pinned inventory. Ctags candidate extraction is not compiler validation, preprocessor configuration, complete syntax coverage, native requirement linkage or behavioral review. Top-level shell effects, C initializers/types/macros and assembly remain separate scope. No script or instrument code is executed.')
    (ROOT/'evidence/c-shell-declaration-candidates.json').write_text(json.dumps(report, indent=2)+'\n')
    print('Candidate inventory:', counts, '; native tracing remains incomplete')
    return report


if __name__ == '__main__':
    audit()
