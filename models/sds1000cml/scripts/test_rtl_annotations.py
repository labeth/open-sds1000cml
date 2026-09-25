import unittest
from rtl_annotations import annotate, token_hash


class RTLCommentsTest(unittest.TestCase):
    def test_preserves_literals_and_baseline_tokens(self):
        source = b'/* module fake; endmodule */\nmodule real; initial $display("module ghost; // hi"); endmodule\n'
        actual = annotate(source, 'FU-RTL', {'real': ['REQ-SDS-047']}, {'REQ-SDS-047'})
        self.assertIn(b'// TRLC-LINKS: REQ-SDS-047\nmodule real;', actual)
        self.assertEqual(token_hash(source), token_hash(actual))
        self.assertNotEqual(token_hash(source), token_hash(source.replace(b'ghost', b'changed')))

    def test_zero_offset_owner_precedes_link(self):
        result = annotate(b'module real; endmodule\n', 'FU-RTL', {'real': ['REQ-SDS-047']}, {'REQ-SDS-047'})
        self.assertTrue(result.startswith(b'// ENGMODEL-OWNER-UNIT: FU-RTL\n// TRLC-LINKS: REQ-SDS-047\nmodule'))

    def test_rejects_incomplete_ambiguous_or_unknown_links(self):
        for source, modules in [(b'module a; endmodule\nmodule b; endmodule', {'a':['REQ-SDS-047']}),
                                (b'module a; endmodule\nmodule a; endmodule', {'a':['REQ-SDS-047']}),
                                (b'module a; endmodule', {'a':['REQ-SDS-999']})]:
            with self.subTest(source=source, modules=modules), self.assertRaises(AssertionError):
                annotate(source, 'FU-RTL', modules, {'REQ-SDS-047'})


if __name__ == '__main__':
    unittest.main()
