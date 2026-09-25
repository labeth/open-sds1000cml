#!/usr/bin/env python3
"""Reject falsely credited legacy variants and altered vendor/continuation evidence."""
# ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
# TRLC-LINKS: REQ-SDS-188, REQ-SDS-043
import contextlib,copy,io,json,shutil,tempfile,unittest
from pathlib import Path
from unittest.mock import patch
from inventory import ROOT
from board_rtl_cases import outcome
import check_board_rtl_evidence as checker

class BoardEvidence(unittest.TestCase):
    def setUp(self):
        self.original=json.loads((ROOT/'evidence/board-rtl-review-test-run.json').read_text())
    def case(self,name):return copy.deepcopy(next(c for c in self.original['cases'] if c['id']==name))
    def test_continuation_values_are_required(self):
        c=self.case('legacy-continuation');self.assertEqual(outcome(c),'partial')
        c['simulationOutput']=c['simulationOutput'].replace('value=32','value=31',1)
        self.assertEqual(outcome(c),'fail')
    def test_missing_continuation_value_is_not_a_pass(self):
        c=self.case('legacy-continuation');lines=c['simulationOutput'].splitlines();i=next(i for i,x in enumerate(lines) if x.startswith('continue '));del lines[i];c['simulationOutput']='\n'.join(lines)
        self.assertEqual(outcome(c),'fail')
    def test_pass_text_does_not_hide_metadata_failure(self):
        c=self.case('legacy-base');c['simulationOutput']+='\n'+c['success']+'\n';self.assertNotEqual(c['simulationExitCode'],0);self.assertEqual(outcome(c),'fail')
    def reject(self,mutate):
        with tempfile.TemporaryDirectory() as tmp:
            root=Path(tmp);(root/'evidence').mkdir()
            for name in ['board-rtl-review-test-run.json','board-core-review-test-run.json','board-metadata-observation.json']:shutil.copyfile(ROOT/'evidence'/name,root/'evidence'/name)
            data=copy.deepcopy(self.original);mutate(data);(root/'evidence/board-rtl-review-test-run.json').write_text(json.dumps(data))
            with patch.object(checker,'ROOT',root),contextlib.redirect_stdout(io.StringIO()):
                with self.assertRaises(AssertionError):checker.check()
    def test_compile_flags_are_part_of_evidence(self):
        self.reject(lambda d:d['cases'][0]['compileCommand'].append('-DINTERLEAVE'))
    def test_vendor_hash_cannot_change(self):
        self.reject(lambda d:next(iter(d['vendorInputs'].values())).update(sha256='0'*64))
    def test_missing_variant_rejected(self):self.reject(lambda d:d['cases'].pop())
if __name__=='__main__':unittest.main()
