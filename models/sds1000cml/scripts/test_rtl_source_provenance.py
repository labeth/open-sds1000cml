"""Historical comment plans must never authorize changed executable inputs."""
import hashlib
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch
import rtl_source_provenance as provenance
from rtl_annotations import annotate


class HistoricalRTLProvenanceTest(unittest.TestCase):
    def test_exact_history_and_fail_closed_mutations(self):
        original = b'module real; wire value = 1; endmodule\n'
        old = {'real': ['REQ-SDS-053']}
        new = {'real': ['REQ-SDS-053', 'REQ-SDS-079']}
        requirements = {'REQ-SDS-053', 'REQ-SDS-079'}
        digest = lambda data: hashlib.sha256(data).hexdigest()
        before = annotate(original, 'FU-RTL', old, requirements)
        current = annotate(original, 'FU-RTL', new, requirements)
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root/'evidence').mkdir()
            (root/'model').mkdir()
            (root/'model/requirements.yml').write_text('requirements:\n- id: REQ-SDS-053\n- id: REQ-SDS-079\n')
            source = root/'source.v'
            source.write_bytes(current)
            def write(name, rows):
                (root/'evidence'/name).write_text(json.dumps({'files': rows}))
            write('rtl-annotation-plan.json', [dict(path='source.v', owner='FU-RTL', modules=new)])
            write('rtl-annotation-history.json', [dict(path='source.v', owner='FU-RTL', modules=old)])
            overlay = dict(path='source.v', baselineSHA256=digest(original), annotatedSHA256=digest(current), exactBaselineRecovery=True)
            write('rtl-annotation-overlay.json', [overlay])
            with patch.multiple(provenance, ROOT=root, SOURCE=root), patch.object(provenance, 'git', return_value=original):
                self.assertTrue(provenance.validate('source.v', digest(before)))
                self.assertTrue(provenance.validate('source.v', digest(original)))
                self.assertFalse(provenance.validate('source.v', digest(current)))
                with self.assertRaises(AssertionError):
                    provenance.validate('source.v', digest(before.replace(b'value = 1', b'value = 0')))
                write('rtl-annotation-history.json', [])
                with self.assertRaises(AssertionError):
                    provenance.validate('source.v', digest(before))
                write('rtl-annotation-history.json', [dict(path='source.v', owner='FU-OTHER', modules=old)])
                with self.assertRaises(AssertionError):
                    provenance.validate('source.v', digest(before))
                source.write_bytes(current.replace(b'value = 1', b'value = 0'))
                overlay['annotatedSHA256'] = digest(source.read_bytes())
                write('rtl-annotation-overlay.json', [overlay])
                with self.assertRaises(AssertionError):
                    provenance.validate('source.v', digest(original))


if __name__ == '__main__':
    unittest.main()
