#!/usr/bin/env python3
"""Require one complete publication diagram per native Mermaid view."""
from collections import Counter
import hashlib
import json
import re
from inventory import ROOT


def expand_edge_key(graph, table):
    rows = re.findall(r'^\|(R\d+) \|([^\n]+)$', table, re.M)
    assert len(dict(rows)) == len(rows), 'duplicate relationship key'
    keys = dict(rows)
    used = set()
    def expand(match):
        label = match.group(1)
        if label in keys:
            used.add(label)
            return '-->|' + keys[label] + '|'
        return match.group(0)
    expanded = re.sub(r'--+>\|([^|\n]+)\|', expand, graph)
    assert used == set(keys), 'unused relationship key'
    return expanded


def contents(graph):
    nodes, edges = set(), Counter()
    for raw in graph.splitlines():
        line = raw.strip()
        if re.match(r'^N_\w+\s+(?:-->|-\.->|==>)', line):
            edges[line] += 1
        else:
            node = re.match(r'^(N_\w+)(?:\[|\(|\{)', line)
            if node:
                nodes.add(node.group(1))
    return nodes, edges


def check():
    document = ROOT / 'generated/ARCHITECTURE.adoc'
    blocks = re.findall(r'\[source,mermaid\]\n----\n(.*?)\n----(.*?)(?=\[source,mermaid\]|\Z)', document.read_text(), re.S)
    parts = {}
    for block, following in blocks:
        identity = re.search(r'^%% view: (\S+) ', block, re.M)
        if identity:
            table = re.match(r'\s*\.Relationship descriptions\n.*?\|===\n(.*?)\n\|===', following, re.S)
            block = expand_edge_key(block, table.group(1) if table else '')
            parts.setdefault(identity.group(1), []).append(block)
    rows = []
    for path in sorted((ROOT / 'generated/views').glob('*.mmd')):
        expected_nodes, expected_edges = contents(path.read_text())
        actual_nodes, actual_edges = set(), Counter()
        assert path.stem in parts, f'missing publication view: {path.stem}'
        assert len(parts[path.stem]) == 1, f'view must remain one complete diagram: {path.stem}'
        for part in parts[path.stem]:
            nodes, edges = contents(part)
            actual_nodes.update(nodes)
            actual_edges.update(edges)
        assert actual_nodes == expected_nodes, f'lost/extra view nodes: {path.stem}'
        assert actual_edges == expected_edges, f'lost/extra view relationships: {path.stem}'
        rows.append(dict(view=path.stem, parts=len(parts[path.stem]), nodes=len(actual_nodes),
                         relationships=sum(actual_edges.values()), sourceSHA256=hashlib.sha256(path.read_bytes()).hexdigest()))
    assert set(parts) == {r['view'] for r in rows}, 'unexpected view diagram'
    report = dict(result='pass', documentSHA256=hashlib.sha256(document.read_bytes()).hexdigest(), views=rows,
                  scope='Native Mermaid node IDs and complete edge-line multisets preserved in one diagram per view. Visual layout and semantic model completeness require separate review.')
    (ROOT / 'evidence/publication-view-parts.json').write_text(json.dumps(report, indent=2)+'\n')
    print(f'PASS: {len(rows)} complete views preserved in {sum(r["parts"] for r in rows)} complete publication diagrams')


if __name__ == '__main__':
    check()
