#!/usr/bin/env python3
"""REQ-SDS-186: reject altered parameter and simulation provenance."""
# ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
# TRLC-LINKS: REQ-SDS-186
import contextlib,io,json,shutil,tempfile,unittest
from pathlib import Path
from unittest.mock import patch
import check_stream_rtl_evidence as checker
from inventory import ROOT

class StreamProvenance(unittest.TestCase):
    def setUp(self):
        self.tmp=tempfile.TemporaryDirectory();self.addCleanup(self.tmp.cleanup)
        self.root=Path(self.tmp.name);(self.root/'evidence').mkdir();(self.root/'model').mkdir()
        for name in ['stream-rtl-review-test-run.json','rtl-annotation-overlay.json','rtl-annotation-plan.json']:
            shutil.copyfile(ROOT/'evidence'/name,self.root/'evidence'/name)
        shutil.copyfile(ROOT/'model/requirements.yml',self.root/'model/requirements.yml')
        self.report=self.root/'evidence/stream-rtl-review-test-run.json'
    def reject(self,mutate):
        data=json.loads(self.report.read_text());mutate(data);self.report.write_text(json.dumps(data))
        with patch.object(checker,'ROOT',self.root), contextlib.redirect_stdout(io.StringIO()):
            with self.assertRaises(AssertionError):checker.check()
    def test_geometry_cannot_change(self):
        self.reject(lambda d:d['cases'][0]['parameters'].update(BANK_AW=4))
    def test_different_compile_inputs_cannot_borrow_results(self):
        self.reject(lambda d:d['cases'][0]['compileCommand'].append('unreviewed.v'))
    def test_pass_marker_does_not_override_nonzero_exit(self):
        self.reject(lambda d:d['cases'][0].update(simulationExitCode=1))
    def test_case_cannot_be_omitted(self):
        self.reject(lambda d:d['cases'].pop())
if __name__=='__main__':unittest.main()
