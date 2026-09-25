#!/usr/bin/env python3
import unittest
from check_acquisition_rtl_evidence import classify
class Outcomes(unittest.TestCase):
 def sample(self,**overrides):
  return dict(dict(compileExitCode=0,simulationExitCode=0,simulationOutput='PASS original assertions\n',timedOut=False,success='PASS original'),**overrides)
 def test_build_failure_is_not_exercised(self):
  self.assertEqual(classify(self.sample(compileExitCode=2,simulationExitCode=None,simulationOutput='')),'not-run')
 def test_build_failure_cannot_claim_simulation(self):
  with self.assertRaises(AssertionError):classify(self.sample(compileExitCode=2))
 def test_nonzero_exit_overrides_success_text(self):
  self.assertEqual(classify(self.sample(simulationExitCode=1)),'fail')
 def test_missing_expected_assertion_marker(self):
  self.assertEqual(classify(self.sample(simulationOutput='PASS unrelated bench\n')),'fail')
 def test_timeout_retains_failure(self):
  self.assertEqual(classify(self.sample(simulationExitCode=124,timedOut=True)),'fail')
 def test_pass_is_bounded_partial_evidence(self):
  self.assertEqual(classify(self.sample()),'partial')
if __name__=='__main__':unittest.main()
