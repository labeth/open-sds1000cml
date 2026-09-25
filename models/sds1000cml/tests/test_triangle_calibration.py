# ENGMODEL-OWNER-UNIT: FU-TOOLS-HW-ADC-SRAM
"""Offline assertions over the actual pinned calibration implementation."""
import hashlib
import importlib.util
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
import warnings
import numpy as np

SOURCE=Path(__file__).resolve().parents[3]/'tools/hw/adc_sram/triangle_calibrate.py'
spec=importlib.util.spec_from_file_location('triangle_under_review',SOURCE)
calibration=importlib.util.module_from_spec(spec)
spec.loader.exec_module(calibration)

# TRLC-LINKS: REQ-SDS-209
def triangle():
    n=np.arange(50000)
    return 128+70*(1-4*np.abs((n/500+.25)%1-.5))


class TriangleContract(unittest.TestCase):
    # TRLC-LINKS: REQ-SDS-209
    def test_ideal_triangle_reference(self):
        result,_=calibration.fit(triangle())
        self.assertAlmostEqual(result['frequency_hz'],1e6,places=5)
        self.assertEqual(result['rail_fraction'],0)
        self.assertEqual([x['slot'] for x in result['cores']],list(range(5)))
        self.assertLess(result['raw_segment_residual_rms_codes'],1e-8)
        for core in result['cores']:
            self.assertAlmostEqual(core['gain'],1,places=9)
            self.assertAlmostEqual(core['offset_codes'],0,places=8)
            self.assertAlmostEqual(core['relative_skew_ns'],0,places=8)

    # TRLC-LINKS: REQ-SDS-209
    def test_held_out_correction_does_not_hide_phase_rotation(self):
        base=triangle();n=np.arange(len(base))
        raw=base*np.array([.97,1.01,1.04,.98,1])[n%5]+np.array([2,-1,.5,-2,1])[n%5]
        fit,_=calibration.fit(raw)
        held=raw+np.random.default_rng(4).normal(0,.1,len(raw))
        result=calibration.validate(held,fit)
        shifted=calibration.validate(np.roll(held,1),fit)
        self.assertGreater(result['raw_segment_residual_rms_codes'],3)
        self.assertLess(result['held_out_corrected_segment_residual_rms_codes'],.12)
        self.assertGreater(shifted['held_out_corrected_segment_residual_rms_codes'],5)

    # TRLC-LINKS: REQ-SDS-209
    def test_cli_local_records_preserve_hashes_and_limits(self):
        with tempfile.TemporaryDirectory() as tmp:
            root=Path(tmp);data=np.column_stack([triangle(),triangle()+4]).round().astype(np.uint8).tobytes()
            files=[root/'fit.bin',root/'held.bin']
            for path in files:path.write_bytes(data)
            out=root/'result.json'
            result=subprocess.run([sys.executable,str(SOURCE),*[str(p) for p in files],'--out',str(out)],capture_output=True,text=True,timeout=30)
            self.assertEqual(result.returncode,0,result.stderr)
            report=json.loads(out.read_text())
            self.assertEqual(report['sample_rate_hz'],500000000)
            self.assertEqual(report['fit_record'],str(files[0]))
            self.assertEqual(len(report['records']),2)
            self.assertTrue(any('record-start core phase' in limit for limit in report['limits']))
            for row,path in zip(report['records'],files):
                self.assertEqual(row['sha256'],hashlib.sha256(data).hexdigest())
                self.assertEqual(path.read_bytes(),data)
                self.assertEqual(len(row['channels']),2)
            self.assertNotIn('held_out_corrected_segment_residual_rms_codes',report['records'][0]['channels'][0])
            self.assertIn('held_out_corrected_segment_residual_rms_codes',report['records'][1]['channels'][0])

    # TRLC-LINKS: REQ-SDS-209
    def test_odd_record_rejected_without_output(self):
        with tempfile.TemporaryDirectory() as tmp:
            path=Path(tmp)/'odd.bin';path.write_bytes(b'abc');out=Path(tmp)/'result.json'
            result=subprocess.run([sys.executable,str(SOURCE),str(path),'--out',str(out)],capture_output=True,text=True,timeout=30)
            self.assertNotEqual(result.returncode,0)
            self.assertIn('AssertionError',result.stderr)
            self.assertFalse(out.exists())
            self.assertEqual(path.read_bytes(),b'abc')

    # TRLC-LINKS: REQ-SDS-209
    def test_constant_record_has_unhandled_crossing_failure(self):
        with warnings.catch_warnings():
            warnings.simplefilter('ignore',RuntimeWarning)
            with self.assertRaises(IndexError):calibration.fit(np.full(50000,128))


if __name__=='__main__':unittest.main()
