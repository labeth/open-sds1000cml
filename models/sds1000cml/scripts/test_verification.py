"""A source test link cannot acquire a pass from an unrelated or conflicting event."""
import unittest
from verify import matching_test_event


class TestResultProvenance(unittest.TestCase):
    def test_only_exact_unambiguous_terminal_result_matches(self):
        event=dict(Package='module/p',Test='TestCapture',Action='pass')
        self.assertTrue(matching_test_event([event],'module/p','TestCapture','pass'))
        self.assertFalse(matching_test_event([event],'module/q','TestCapture','pass'))
        self.assertFalse(matching_test_event([event],'module/p','TestOther','pass'))
        self.assertFalse(matching_test_event([dict(event,Action='skip')],'module/p','TestCapture','pass'))
        self.assertFalse(matching_test_event([event,dict(event,Action='fail')],'module/p','TestCapture','pass'))
        self.assertFalse(matching_test_event([],'module/p','TestCapture','pass'))


if __name__ == '__main__':
    unittest.main()
