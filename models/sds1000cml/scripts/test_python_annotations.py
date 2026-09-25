import unittest
from python_annotations import annotate,tokens


class PythonComments(unittest.TestCase):
    def test_preserves_encoding_docstrings_decorators_and_nested_tokens(self):
        source=b'#!/usr/bin/python3\n# coding: utf-8\n"""docstring"""\n@decorate(\n    1,\n)\ndef outer():\n    def inner(): return "# unchanged"\n    return inner\n'
        row=dict(path='test.py',owner='FU-PY',functions={'outer':['REQ-PY-001'],'outer.inner':['REQ-PY-001']})
        result=annotate(source,row,{'REQ-PY-001'})
        self.assertTrue(result.startswith(b'#!/usr/bin/python3\n# coding: utf-8\n# ENGMODEL-OWNER'))
        self.assertIn(b'# TRLC-LINKS: REQ-PY-001\n@decorate(',result)
        self.assertEqual(tokens(source),tokens(result))

    def test_class_cannot_be_counted_as_a_function(self):
        source=b'class A:\n    def f(self): pass\n'
        row=dict(path='a.py',owner='FU-PY',functions={'A':['REQ-PY-001'],'A.f':['REQ-PY-001']})
        with self.assertRaises(AssertionError):annotate(source,row,{'REQ-PY-001'})
        row['classes']={'A':row['functions'].pop('A')}
        self.assertEqual(tokens(source),tokens(annotate(source,row,{'REQ-PY-001'})))

    def test_rejects_incomplete_or_unknown_links(self):
        for funcs in ({},{'one':['REQ-PY-002']}):
            with self.assertRaises(AssertionError):
                annotate(b'def one(): pass\n',dict(path='a.py',owner='FU-PY',functions=funcs),{'REQ-PY-001'})


if __name__=='__main__':unittest.main()
