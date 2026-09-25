#!/usr/bin/env python3
"""Read a pinned local Git tree; write reproducible, exhaustive source evidence.
Never checks out, alters, builds or contacts the instrument/source remote.
"""
import argparse, collections, hashlib, json, re, subprocess
from pathlib import Path
ROOT = Path(__file__).resolve().parents[1]
SOURCE = ROOT.parents[1]
REV = 'f43696305a7957b9956e3b1ac425cdf323edde84'
BRANCH = 'cyclone-spec-sram-capture'
def git(*args):
    return subprocess.check_output(['git', '-C', str(SOURCE), *args])
def read(path):
    return git('show', f'{REV}:{path}').decode('utf-8', errors='replace')
def ident(s):
    return re.sub('[^A-Z0-9]+', '-', s.upper()).strip('-')
def write(name, obj):
    (ROOT / 'evidence' / name).write_text(json.dumps(obj, indent=2, sort_keys=True)+'\n')
def category(path):
    if path.startswith('validation/'): return 'historical-validation'
    if '/testdata/' in path or '/fixtures/' in path or path.endswith(('.bin','.rbf','.ko','.png','.jpg','.svg','.wav','.zip','.pdf')): return 'asset-or-fixture'
    if path.endswith('_test.go') or '/sim/' in path or '/e2e/' in path or re.search(r'(^|/)(test_|tb_)|\.test\.',path): return 'test'
    if path.endswith(('.go','.v','.vh','.c','.h','.s','.S','.js','.ts','.html','.css')): return 'implementation'
    if path.endswith(('.md','.txt')): return 'documentation'
    if path.endswith(('.py','.sh','.tcl','.qsf','.sdc','.yml','.yaml','.mod','.sum','.json')) or Path(path).name in ('Makefile','.gitignore','.gitattributes'): return 'build-or-configuration'
    return 'other-support'
def owner(path):
    parts=path.split('/')
    if path.startswith('validation/'): return 'FU-VALIDATION-EVIDENCE'
    if path.startswith('specs/'): return 'FU-SPECIFICATIONS'
    if path.startswith('docs/'): return 'FU-DOCUMENTATION'
    if path.startswith('fpga/acq_sram/sim/'): return 'FU-RTL-ACQ-TESTS'
    if path.startswith('fpga/') and path.endswith('.v') and '/sim/' not in path:
        return 'FU-RTL-'+ident(path[5:-2])
    if path.startswith('fpga/common/sim/'): return 'FU-RTL-COMMON-TESTS'
    if path.startswith('fpga/default/sim/'): return 'FU-RTL-DEFAULT-TESTS'
    if path.startswith('fpga/sram_bench/sim/'): return 'FU-RTL-BENCH-TESTS'
    if path.startswith('fpga/acq_sram/'): return 'FU-FPGA-ACQ-BUILD'
    if path.startswith('fpga/default/'): return 'FU-FPGA-DEFAULT-BUILD'
    if path.startswith('fpga/sram_bench/'): return 'FU-FPGA-BENCH-BUILD'
    if path.startswith('app/internal/web/'):
        if path.endswith('.js') and '/e2e/' not in path: return 'FU-WEB-'+ident(Path(path).stem)
        return 'FU-APP-WEB'
    if path.startswith(('app/internal/','app/cmd/','ota/internal/','ota/cmd/','codegen/cmd/','fpga/internal/','fpga/cmd/')):
        return 'FU-'+ident('/'.join([parts[0],parts[2]]))
    if path.startswith('codegen/') and len(parts)>2: return 'FU-CODEGEN-'+ident(parts[1])
    if path.startswith('ota/boot/'): return 'FU-OTA-BOOT'
    if path.startswith('tools/'):
        return 'FU-TOOLS-'+ident('/'.join(parts[1:3])) if len(parts)>3 else 'FU-TOOLS'
    if path.startswith('app/'): return 'FU-APP-BUILD'
    if path.startswith('ota/'): return 'FU-OTA-BUILD'
    if path.startswith('codegen/'): return 'FU-CODEGEN-BUILD'
    if path.startswith('fpga/'): return 'FU-FPGA-BUILD'
    return 'FU-REPOSITORY'
def rtl_dependencies(modules, texts):
    edges=[]
    for module in modules:
        if module['simulation']:continue
        raw=texts[module['path']]
        # Preserve offsets while masking comments; helper modules in the same
        # file must not inherit the top module's instantiations.
        text=re.sub(r'/\*.*?\*/|//[^\n]*',lambda m:re.sub(r'[^\n]',' ',m.group()),raw,flags=re.S)
        start=re.search(r'(?m)^\s*module\s+'+re.escape(module['name'])+r'\b',text)
        if not start:raise ValueError('missing module '+module['name'])
        end=re.search(r'\bendmodule\b',text[start.end():])
        if not end:raise ValueError('unterminated module '+module['name'])
        body_start=start.end();body=text[body_start:body_start+end.start()]
        seen=set()
        for child in modules:
            if child['simulation']:continue
            for match in re.finditer(r'(?m)^[ \t]*'+re.escape(child['name'])+r'\s+(?:#\s*\(|\w+\s*\()',body):
                line=text.count('\n',0,body_start+match.start())+1
                key=(child['name'],child['owner'],line)
                if key in seen:continue
                seen.add(key)
                edges.append(dict(source=module['owner'],target=child['owner'],sourceModule=module['name'],
                                  targetModule=child['name'],path=module['path'],line=line,
                                  internal=module['owner']==child['owner'],kind='lexical-instantiation'))
    return edges

def collect():
    records=[]; texts={};units=collections.defaultdict(list)
    for raw in git('ls-tree','-rlz',REV).split(b'\0'):
        if not raw: continue
        header,path=raw.split(b'\t',1);mode,kind,blob,size=header.decode().split()
        path=path.decode(); row=dict(path=path,mode=mode,kind=kind,gitBlob=blob,bytes=int(size),category=category(path),owner=owner(path))
        records.append(row);units[row['owner']].append(path)
        if row['category'] not in ('historical-validation','asset-or-fixture') and Path(path).suffix in ('.go','.v','.c','.js','.md','.qsf','.sdc','.mod','.py','.sh','.vh'):
            texts[path]=read(path)
    symbols=[];rtl=[];imports=[]
    module_roots={p.rsplit('/',1)[0]:re.search(r'^module\s+(\S+)',t,re.M).group(1) for p,t in texts.items() if p.endswith('/go.mod')}
    for p,t in texts.items():
        own=owner(p)
        if p.endswith('.go'):
            for m in re.finditer(r'^func\s+(?:\([^\n]*?\)\s+)?([\w]+)\s*(?:\[.*?\])?\(',t,re.M):
                symbols.append(dict(path=p,line=t.count('\n',0,m.start())+1,name=m.group(1),owner=own,kind='go-test' if p.endswith('_test.go') else 'go-function'))
            import_blocks='\n'.join(m.group(1) or m.group(2) for m in re.finditer(r'^import\s+(?:\((.*?)\)|([^\n]+))',t,re.M|re.S))
            for m in re.finditer(r'"([\w.\-/]+)"',import_blocks):
                for root,mod in module_roots.items():
                    if m.group(1).startswith(mod+'/'):
                        target=root+'/'+m.group(1)[len(mod)+1:]
                        # Import identity comes from exact declared module paths.
                        candidates=[x for x in records if str(Path(x['path']).parent)==target and x['path'].endswith('.go')]
                        if candidates: imports.append(dict(source=own,target=candidates[0]['owner'],path=p,importPath=m.group(1),test=p.endswith('_test.go')))
        if p.endswith('.v'):
            for m in re.finditer(r'^module\s+(\w+)',t,re.M):
                rtl.append(dict(path=p,line=t.count('\n',0,m.start())+1,name=m.group(1),owner=own,simulation='/sim/' in p,header=t[m.start():t.find(');',m.end())+2]))
    edges=rtl_dependencies(rtl,texts)
    pins=[]
    for p,t in texts.items():
        if p.endswith('.qsf'):
            for m in re.finditer(r'set_location_assignment PIN_(\w+) -to (\S+)',t):pins.append(dict(path=p,line=t.count('\n',0,m.start())+1,ball=m.group(1),port=m.group(2)))
    provenance=dict(branch=BRANCH,commit=REV,tree=git('rev-parse',REV+'^{tree}').decode().strip(),repository='../..',scope='All committed paths at the pinned commit. Untracked files are excluded.',files=len(records),categories=dict(collections.Counter(x['category'] for x in records)),inventorySHA256=hashlib.sha256(json.dumps(records,sort_keys=True).encode()).hexdigest())
    write('source-manifest.json',provenance);write('source-inventory.json',records)
    write('source-symbols.json',symbols);write('rtl-modules.json',rtl);write('dependencies.json',dict(goImports=imports,rtlInstantiationCandidates=edges));write('pin-assignments.json',pins)
    write('source-units.json',dict(sorted(units.items())))
    print(json.dumps(dict(files=len(records),units=len(units),symbols=len(symbols),rtlModules=len(rtl),pins=len(pins),categories=provenance['categories']),indent=2))
    return records,texts,units
if __name__=='__main__':collect()
