"""Regression checks that annotation application refuses stale or ambiguous inputs."""
import contextlib
import io
import json
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

import annotations


class AnnotationSafety(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        root = Path(self.temp.name)
        self.root = root / 'model'
        self.source = root / 'source'
        self.source.mkdir()
        for directory in ['evidence', 'model', 'scripts/helpers']:
            (self.root / directory).mkdir(parents=True)
        (self.root / 'scripts/helpers/go-symbols.go.txt').write_bytes(
            (annotations.ROOT / 'scripts/helpers/go-symbols.go.txt').read_bytes())
        self.original = b'package sample\n\nfunc capture() {}\n'
        self.target = self.source / 'sample.go'
        self.target.write_bytes(self.original)
        (self.root / 'model/requirements.yml').write_text('requirements:\n- id: REQ-SDS-007\n')
        (self.root / 'evidence/source-units.json').write_text('{"FU-SAMPLE": ["sample.go"]}')
        self.plan = dict(scope='test fixture', files=[dict(path='sample.go', owner='FU-SAMPLE',
                         functions={'capture': ['REQ-SDS-007']})])

    def run_plan(self, apply=False):
        (self.root / 'evidence/annotation-plan.json').write_text(json.dumps(self.plan))
        with patch.object(annotations, 'ROOT', self.root), patch.object(annotations, 'SOURCE', self.source), \
             patch.object(annotations, 'git', return_value=self.original), contextlib.redirect_stdout(io.StringIO()):
            return annotations.run(apply)

    def test_apply_is_idempotent_and_recovers_baseline(self):
        first = self.run_plan(True)
        self.assertEqual(first, self.run_plan(True))
        self.assertEqual(first, self.run_plan())
        self.assertTrue(first['files'][0]['exactBaselineRecovery'])

    def test_check_does_not_apply_missing_annotations(self):
        with self.assertRaisesRegex(AssertionError, 'unexpected worktree'):
            self.run_plan()
        self.assertEqual(self.target.read_bytes(), self.original)

    def test_apply_refuses_source_edits(self):
        changed = self.original.replace(b'capture() {}', b'capture() { panic("changed") }')
        self.target.write_bytes(changed)
        with self.assertRaisesRegex(AssertionError, 'unexpected worktree'):
            self.run_plan(True)
        self.assertEqual(self.target.read_bytes(), changed)

    def test_unknown_requirement_is_rejected(self):
        self.plan['files'][0]['functions']['capture'] = ['REQ-UNKNOWN']
        with self.assertRaisesRegex(AssertionError, 'unknown requirement'):
            self.run_plan(True)
        self.assertEqual(self.target.read_bytes(), self.original)

    def test_ambiguous_declaration_is_rejected(self):
        self.original = b'package sample\ntype A int\ntype B int\nfunc (A) capture() {}\nfunc (B) capture() {}\n'
        self.target.write_bytes(self.original)
        with self.assertRaisesRegex(AssertionError, 'ambiguous or absent'):
            self.run_plan(True)
        self.assertEqual(self.target.read_bytes(), self.original)

    def test_receiver_qualified_links_disambiguate_methods(self):
        self.original = b'package sample\ntype A int\ntype B int\nfunc (A) capture() {}\nfunc (*B) capture() {}\n'
        self.target.write_bytes(self.original)
        self.plan['files'][0]['functions'] = {'A.capture': ['REQ-SDS-007'], 'B.capture': ['REQ-SDS-007']}
        self.assertEqual(self.run_plan(True)['linkedDeclarations'], 2)

    def test_package_and_method_names_require_explicit_disambiguation(self):
        self.original = b'package sample\ntype A int\nfunc (A) capture() {}\nfunc capture() {}\n'
        self.target.write_bytes(self.original)
        with self.assertRaisesRegex(AssertionError, 'ambiguous or absent'):
            self.run_plan(True)
        self.assertEqual(self.target.read_bytes(), self.original)
        self.plan['files'][0]['functions'] = {'A.capture': ['REQ-SDS-007'], 'package.capture': ['REQ-SDS-007']}
        report = self.run_plan(True)
        self.assertEqual(report['linkedDeclarations'], 2)
        self.assertTrue(report['files'][0]['exactBaselineRecovery'])
        self.assertEqual(report, self.run_plan())

    def test_reviewed_overlay_can_be_extended(self):
        self.original += b'func consume() {}\n'
        self.target.write_bytes(self.original)
        self.run_plan(True)
        self.plan['files'][0]['functions']['consume'] = ['REQ-SDS-007']
        self.assertEqual(self.run_plan(True)['linkedDeclarations'], 2)
        self.assertEqual(self.run_plan()['linkedDeclarations'], 2)

    def test_type_only_and_grouped_contracts_recover_exact_baseline(self):
        self.original = b'package sample\n\ntype Request struct { Cmd string }\ntype (\n Response struct { OK bool }\n Alias = Request\n)\n'
        self.target.write_bytes(self.original)
        self.plan['files'][0]['functions'] = {}
        self.plan['files'][0]['types'] = {'Request': ['REQ-SDS-007'], 'Response,Alias': ['REQ-SDS-007']}
        report = self.run_plan(True)
        self.assertEqual(report['linkedDeclarations'], 0)
        self.assertEqual(report['linkedTypeDeclarations'], 2)
        self.assertEqual(report['files'][0]['totalTypeDeclarations'], 2)
        self.assertTrue(report['files'][0]['exactBaselineRecovery'])
        self.assertEqual(self.target.read_bytes().count(b'// TRLC-LINKS:'), 2)
        self.assertEqual(report, self.run_plan())

    def test_partial_group_name_is_rejected_without_changes(self):
        self.original = b'package sample\ntype ( A int; B int )\n'
        self.target.write_bytes(self.original)
        self.plan['files'][0]['functions'] = {}
        self.plan['files'][0]['types'] = {'A': ['REQ-SDS-007']}
        with self.assertRaisesRegex(AssertionError, 'absent type declaration'):
            self.run_plan(True)
        self.assertEqual(self.target.read_bytes(), self.original)

    def test_user_comment_after_overlay_is_preserved_by_rejection(self):
        self.run_plan(True)
        changed = self.target.read_bytes() + b'// user note\n'
        self.target.write_bytes(changed)
        with self.assertRaisesRegex(AssertionError, 'unexpected worktree'):
            self.run_plan(True)
        self.assertEqual(self.target.read_bytes(), changed)


if __name__ == '__main__':
    unittest.main()
