#!/usr/bin/env python3
"""Audit authored Python links across every pinned nonhistorical Python file."""
import hashlib,json,os,subprocess,tempfile
from pathlib import Path
from inventory import ROOT,SOURCE,REV


def audit():
    inventory=json.loads((ROOT/'evidence/python-declaration-inventory.json').read_text())
    hashes={}
    with tempfile.TemporaryDirectory(prefix='sds-python-native-') as tmp:
        staging=Path(tmp)/'source'
        for row in inventory['files']:
            path=row['path'];data=(SOURCE/path).read_bytes()
            from python_source_provenance import validate
            validate(path,row['sourceSHA256'])
            hashes[path]=hashlib.sha256(data).hexdigest()
            target=staging/path;target.parent.mkdir(parents=True,exist_ok=True);target.write_bytes(data)
        helper=Path(tmp)/'scan.go';helper.write_bytes((ROOT/'scripts/helpers/verilog-scan.go.txt').read_bytes())
        tool=os.environ.get('ENGINEERING_MODEL_GO_DIR',str(ROOT.parents[2]/'engineering-model-go'))
        run=subprocess.run(['go','run',str(helper),str(staging)],cwd=tool,env={**os.environ,'GOPROXY':'off'},capture_output=True,text=True,check=True)
        result=json.loads(run.stdout)
    diagnostics=result.get('diagnostics') or []
    assert all(d['code']=='code.missing_trlc_link' for d in diagnostics),diagnostics
    overlay=json.loads((ROOT/'evidence/python-annotation-overlay.json').read_text())
    assert len(result['symbols'])==overlay['linkedFunctions']+sum(r['classes'] for r in overlay['files'])
    report=dict(status='incomplete' if diagnostics else 'pass',commit=REV,files=len(hashes),sourceSHA256=hashes,linkedDeclarations=len(result['symbols']),inventoriedFunctions=inventory['counts']['functions'],inventoriedClasses=inventory['counts']['classes'],symbols=result['symbols'],diagnostics=diagnostics,scope='Actual authored comments in current approved source overlays. Missing links remain explicit across the whole nonhistorical Python inventory. No synthetic parser-test markers, script execution or top-level behavior coverage are credited.')
    (ROOT/'evidence/python-native-trace-audit.json').write_text(json.dumps(report,indent=2)+'\n')
    print('Python native audit:',len(result['symbols']),'authored declaration links;',len(diagnostics),'files still report missing function links')
    return report


if __name__=='__main__':audit()
