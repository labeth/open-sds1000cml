import unittest
from audit_python import declarations


class PythonInventoryTest(unittest.TestCase):
    def test_nested_decorated_async_and_conditional_declarations(self):
        source = b'''@decorate
class A:
    @decorate
    async def work(self):
        def nested(): pass
        return lambda: 1
if False:
    def conditional(): pass
'''
        rows = declarations(source, 'fixture.py')
        self.assertEqual([(r['name'], r['kind'], r['line']) for r in rows],
                         [('A', 'class', 2), ('A.work', 'async-function', 4),
                          ('A.work.nested', 'function', 5), ('conditional', 'function', 8)])
        self.assertEqual(rows[0]['decoratorLines'], [1])
        self.assertEqual(rows[1]['decoratorLines'], [3])

    def test_syntax_error_is_not_silently_skipped(self):
        with self.assertRaises(SyntaxError):
            declarations(b'def unfinished(', 'broken.py')


if __name__ == '__main__':
    unittest.main()
