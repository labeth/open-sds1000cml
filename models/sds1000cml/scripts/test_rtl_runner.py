"""Evidence must identify compilation inputs, not later worktree contents."""
import hashlib
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch
import run_rtl_tests as runner


class SourceSnapshotTest(unittest.TestCase):
    def test_worktree_edit_during_compile_cannot_change_evidence(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            source = root / 'worktree'
            source.mkdir()
            original = b'module tb; endmodule\n'
            (source / 'tb.v').write_bytes(original)
            evidence = root / 'model/evidence/test-runs'
            evidence.mkdir(parents=True)
            (evidence / 'results.json').write_text('[]')
            case = dict(id='snapshot-test', test='tb.v', sources=[], top='tb',
                        requirements=['REQ-SDS-044'], success='PASS snapshot')
            directories = []

            def execute(command, **options):
                cwd = Path(options['cwd'])
                directories.append(cwd)
                self.assertNotEqual(cwd, source)
                self.assertEqual((cwd / 'tb.v').read_bytes(), original)
                if command[0] == 'iverilog':
                    (source / 'tb.v').write_bytes(b'changed during compilation')
                return subprocess.CompletedProcess(command, 0, 'PASS snapshot\n', '')

            with patch.object(runner, 'SOURCE', source), patch.object(runner, 'ROOT', root / 'model'), \
                    patch.object(runner, 'CASES', [case]), patch.object(runner.subprocess, 'run', execute):
                runner.run()
            report = json.loads((evidence / 'rtl-reviewed.json').read_text())
            row = report['cases'][0]
            self.assertTrue(row['sourceSnapshot'])
            self.assertEqual(row['sha256']['tb.v'], hashlib.sha256(original).hexdigest())
            self.assertEqual(directories[0], directories[1])
            self.assertEqual(row['workingDirectory'], str(directories[0]))

    def test_rejects_paths_outside_snapshot(self):
        with tempfile.TemporaryDirectory() as tmp:
            for name in ['../outside.v', '/absolute.v']:
                with self.subTest(name=name), self.assertRaises(ValueError):
                    runner.snapshot_sources(Path(tmp), Path(tmp) / 'snapshot',
                                            [dict(test=name, sources=[])])


if __name__ == '__main__':
    unittest.main()
