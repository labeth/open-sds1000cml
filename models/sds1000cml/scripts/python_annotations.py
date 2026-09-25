#!/usr/bin/env python3
"""Apply reviewed Python comments with exact recovery, AST and token identity."""
import argparse,ast,hashlib,io,json,re,tokenize
import yaml
from inventory import ROOT,SOURCE,REV,git
from audit_python import declarations


def digest(data):
    return hashlib.sha256(data).hexdigest()


def tokens(data):
    return [(t.type,t.string) for t in tokenize.tokenize(io.BytesIO(data).readline)
            if t.type not in (tokenize.COMMENT,tokenize.NL,tokenize.ENCODING)]


def annotate(original, row, requirements):
    decls=declarations(original,row['path'])
    assert set(row['functions'])=={d['name'] for d in decls if d['kind']!='class'}, 'function inventory mismatch'
    assert set(row.get('classes',{}))=={d['name'] for d in decls if d['kind']=='class'}, 'class inventory mismatch'
    links={**row['functions'],**row.get('classes',{})}
    assert set(links)=={d['name'] for d in decls}, 'plan must cover all declarations in a reviewed file'
    lines=original.splitlines(keepends=True)
    header=1 if lines and lines[0].startswith(b'#!') else 0
    for i,line in enumerate(lines[:2]):
        if re.match(rb'^\s*#.*coding[:=]\s*[-\w.]+',line):header=max(header,i+1)
    inserts=[(sum(map(len,lines[:header])),f"# ENGMODEL-OWNER-UNIT: {row['owner']}\n".encode())]
    for d in decls:
        refs=links[d['name']]
        assert refs and set(refs)<=requirements,d
        line=min([d['line'],*d['decoratorLines']])-1
        indent=lines[line][:len(lines[line])-len(lines[line].lstrip())]
        inserts.append((sum(map(len,lines[:line])),indent+('# TRLC-LINKS: '+', '.join(refs)+'\n').encode()))
    ordered=sorted(enumerate(inserts),key=lambda x:(x[1][0],x[0]))
    expected=original
    for _,(offset,comment) in reversed(ordered):expected=expected[:offset]+comment+expected[offset:]
    shift=0;positions=[]
    for _,(offset,comment) in ordered:positions.append((offset+shift,comment));shift+=len(comment)
    recovered=expected
    for offset,comment in reversed(positions):
        assert recovered[offset:offset+len(comment)]==comment
        recovered=recovered[:offset]+recovered[offset+len(comment):]
    assert recovered==original
    assert tokens(expected)==tokens(original),'Python executable tokens changed'
    assert ast.dump(ast.parse(expected))==ast.dump(ast.parse(original)),'Python AST changed'
    return expected


def run(apply=False):
    plan=json.loads((ROOT/'evidence/python-annotation-plan.json').read_text())
    owners=json.loads((ROOT/'evidence/source-units.json').read_text())
    requirements={r['id'] for r in yaml.safe_load((ROOT/'model/requirements.yml').read_text())['requirements']}
    target=ROOT/'evidence/python-annotation-overlay.json'
    previous={r['path']:r for r in json.loads(target.read_text())['files']} if target.exists() else {}
    pending=[];rows=[]
    for row in plan['files']:
        path=row['path'];assert path.endswith('.py') and path in owners[row['owner']]
        original=git('show',f'{REV}:{path}');expected=annotate(original,row,requirements);current=(SOURCE/path).read_bytes()
        prior=previous.get(path,{})
        approved=prior.get('baselineSHA256')==digest(original) and prior.get('annotatedSHA256')==digest(current)
        assert current==expected or (apply and (current==original or approved)), 'unapproved Python edit: '+path
        pending.append((SOURCE/path,expected));rows.append(dict(path=path,baselineSHA256=digest(original),annotatedSHA256=digest(expected),functions=len(row['functions']),classes=len(row.get('classes',{})),exactBaselineRecovery=True,tokenIdentity=True,astIdentity=True))
    if apply:
        for path,data in pending:path.write_bytes(data)
    for path,data in pending:assert path.read_bytes()==data
    report=dict(commit=REV,result='pass',files=rows,linkedFunctions=sum(r['functions'] for r in rows),scope='Only reviewed comments added; exact baseline bytes recover and both tokens and AST remain unchanged. No device script is executed.')
    target.write_text(json.dumps(report,indent=2)+'\n');print('PASS: Python overlays:',report['linkedFunctions'],'functions in',len(rows),'files')
    return report


if __name__=='__main__':
    p=argparse.ArgumentParser();p.add_argument('--apply',action='store_true');run(p.parse_args().apply)
