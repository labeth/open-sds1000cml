"""Keep module-level source evidence distinct from unit-level architecture."""
import unittest
from inventory import rtl_dependencies


class RTLDependencies(unittest.TestCase):
    def test_scope_comments_and_internal_helpers(self):
        text = '''module top();
 helper real_helper();
 // outside comment_instance();
 /* outside also_not_real(); */
endmodule
module helper();
 outside real_child();
endmodule
module outside(); endmodule
'''
        modules = [dict(path='x.v', name=name, owner=owner, simulation=False)
                   for name, owner in [('top', 'FU-TOP'), ('helper', 'FU-TOP'), ('outside', 'FU-OUT')]]
        edges = rtl_dependencies(modules, {'x.v': text})
        self.assertEqual([(r['sourceModule'], r['targetModule'], r['line'], r['internal']) for r in edges],
                         [('top', 'helper', 2, True), ('helper', 'outside', 7, False)])


if __name__ == '__main__':
    unittest.main()
