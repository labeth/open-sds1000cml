import unittest
from trace_audit import requirement_gaps, needs_non_go_review, blocks_declaration_review


class RequirementGapsTest(unittest.TestCase):
    def test_language_independent_and_proposed_aware(self):
        requirements = {'REQ-HDL-001': {'status':'draft','verificationMethods':['test']},
                        'REQ-HW-002': {'status':'draft','verificationMethods':['inspection']},
                        'REQ-FUTURE-003': {'status':'proposed','verificationMethods':['test']}}
        proposed, implementation, verification = requirement_gaps(requirements, {'REQ-HDL-001'}, {'REQ-HDL-001'})
        self.assertEqual(proposed, {'REQ-FUTURE-003'})
        self.assertEqual(implementation, ['REQ-HW-002'])
        self.assertEqual(verification, [])
        _, implementation, verification = requirement_gaps(requirements, set(), set())
        self.assertEqual(implementation, ['REQ-HDL-001','REQ-HW-002'])
        self.assertEqual(verification, ['REQ-HDL-001'])

    def test_active_language_scope(self):
        for suffix in ('.ts', '.tsx', '.v', '.sv'):
            with self.subTest(suffix=suffix):
                self.assertTrue(needs_non_go_review(dict(path='helpers/driver'+suffix, category='other-support')))
                self.assertFalse(needs_non_go_review(dict(path='validation/driver'+suffix, category='historical-validation')))
        for suffix in ('.py', '.sh', '.tcl', '.c', '.s', '.js', '.mjs', '.cjs', '.go', '.md'):
            with self.subTest(suffix=suffix):
                self.assertFalse(needs_non_go_review(dict(path='source'+suffix, category='implementation')))

    def test_only_expression_warnings_are_separate_from_declaration_gaps(self):
        self.assertFalse(blocks_declaration_review({'code':'code.verilog_expression_macro','severity':'warning'}))
        for code, severity in [('code.verilog_expression_macro','error'), ('code.missing_trlc_link','error'), ('code.parse_error','warning')]:
            self.assertTrue(blocks_declaration_review({'code':code,'severity':severity}))
