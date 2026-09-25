"""Result imports must fail closed on stale or ambiguous evidence."""
import hashlib
import json
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch
import go_evidence


class GoEvidenceTests(unittest.TestCase):
    def setUp(self):
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        self.root = Path(tmp.name) / 'model'
        self.source = Path(tmp.name) / 'source'
        self.path = 'app/pkg/capture_test.go'
        self.log = 'test-runs/capture.jsonl'
        (self.root / 'evidence/test-runs').mkdir(parents=True)
        (self.source / self.path).parent.mkdir(parents=True)
        (self.source / self.path).write_text('package pkg\n')
        self.events = [dict(Package='module/pkg', Test='TestCapture', Action='pass')]
        self.row = dict(path=self.path, test='TestCapture', log=self.log, result='pass', requirements=['REQ-SDS-007'])
        self.write('reviewed-test-links.json', [self.row])
        self.write('annotation-plan.json', dict(files=[dict(path=self.path, functions={'TestCapture': ['REQ-SDS-007']})]))
        self.write('annotation-overlay.json', dict(files=[dict(path=self.path, annotatedSHA256=hashlib.sha256((self.source/self.path).read_bytes()).hexdigest())]))
        self.write('source-inventory.json', [dict(path='app/go.mod')])
        self.log_events()

    def write(self, name, obj):
        (self.root/'evidence'/name).write_text(json.dumps(obj))

    def log_events(self):
        data=''.join(json.dumps(x)+'\n' for x in self.events).encode()
        (self.root/'evidence'/self.log).write_bytes(data)
        self.write('test-runs/results.json', [dict(log='capture.jsonl', commit=go_evidence.REV, repository='open-sds1000cml-acq2', sha256=hashlib.sha256(data).hexdigest())])

    def expected(self):
        with patch.object(go_evidence,'ROOT',self.root), patch.object(go_evidence,'SOURCE',self.source), patch.object(go_evidence,'git',return_value=b'module module\n'):
            return go_evidence.expected()

    def test_passing_test_is_partial_requirement_evidence(self):
        row=self.expected()['app/pkg/capture_test.json']
        self.assertEqual(row['results'][0]['status'],'partial')
        self.assertEqual(row['tests'][0]['name'],'TestCapture')

    def test_changed_log_is_rejected(self):
        (self.root/'evidence'/self.log).write_text('{}\n')
        with self.assertRaisesRegex(AssertionError,'stale log'): self.expected()

    def test_changed_source_is_rejected(self):
        (self.source/self.path).write_text('package changed\n')
        with self.assertRaisesRegex(AssertionError,'stale source'): self.expected()

    def test_wrong_package_cannot_supply_outcome(self):
        self.events[0]['Package']='another/pkg'; self.log_events()
        with self.assertRaisesRegex(AssertionError,'outcome mismatch'): self.expected()

    def test_repeated_terminal_outcomes_are_rejected(self):
        self.events.append(self.events[0].copy()); self.log_events()
        with self.assertRaisesRegex(AssertionError,'outcome mismatch'): self.expected()


if __name__ == '__main__':
    unittest.main()
