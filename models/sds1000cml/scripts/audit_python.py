#!/usr/bin/env python3
"""Inventory pinned Python declarations without executing instrument scripts.

This is an independent AST inventory, not native engineering-model-go tracing.
"""
import ast
import hashlib
import json
from collections import Counter
from inventory import ROOT, SOURCE, REV, git


def declarations(data, filename):
    tree = ast.parse(data, filename=filename)
    rows = []
    class Visitor(ast.NodeVisitor):
        def __init__(self):
            self.parents = []
        def declaration(self, node, kind):
            name = '.'.join([*self.parents, node.name])
            rows.append(dict(name=name, kind=kind, line=node.lineno, endLine=node.end_lineno,
                             decoratorLines=[x.lineno for x in node.decorator_list]))
            self.parents.append(node.name)
            self.generic_visit(node)
            self.parents.pop()
        def visit_FunctionDef(self, node):
            self.declaration(node, 'function')
        def visit_AsyncFunctionDef(self, node):
            self.declaration(node, 'async-function')
        def visit_ClassDef(self, node):
            self.declaration(node, 'class')
    Visitor().visit(tree)
    return sorted(rows, key=lambda row: (row['line'], row['name']))


def audit():
    inventory = json.loads((ROOT/'evidence/source-inventory.json').read_text())
    files = []
    for item in inventory:
        path = item['path']
        if not path.endswith('.py') or item['category'] == 'historical-validation':
            continue
        data = git('show', f'{REV}:{path}')
        if (SOURCE/path).read_bytes() != data:
            from python_source_provenance import validate
            validate(path, hashlib.sha256(data).hexdigest())
        symbols = declarations(data, path)
        files.append(dict(path=path, owner=item['owner'], gitBlob=item['gitBlob'],
                          sourceSHA256=hashlib.sha256(data).hexdigest(), declarations=symbols))
    counts = Counter(row['kind'] for file in files for row in file['declarations'])
    report = dict(status='native-tracing-incomplete', commit=REV, files=files,
                  counts=dict(files=len(files), functions=counts['function']+counts['async-function'],
                              classes=counts['class']),
                  scope='All nonhistorical .py files in the pinned inventory, parsed without execution. Includes nested functions, methods, async functions and classes; excludes lambdas and top-level effects. This does not claim reviewed behavior, native requirement links or device qualification.')
    (ROOT/'evidence/python-declaration-inventory.json').write_text(json.dumps(report, indent=2)+'\n')
    print('Python AST inventory:', report['counts'], '; native tracing remains incomplete')
    return report


if __name__ == '__main__':
    audit()
