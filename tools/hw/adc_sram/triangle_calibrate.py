#!/usr/bin/env python3
"""Fit relative interleave errors from a triangle; validate on separate records.
No device access. Residuals include source error; they are not sine-wave ENOB.
"""
import argparse, hashlib, json, pathlib
import numpy as np
from scipy.ndimage import uniform_filter1d


def fit(x, fs=500e6):
    x=x.astype(float);n=np.arange(len(x));smooth=uniform_filter1d(x,15)
    lo,hi=np.percentile(smooth,[2,98]);mid=(lo+hi)/2
    cross=np.flatnonzero((smooth[:-1]<mid)&(smooth[1:]>=mid))
    cross=cross[(cross>30)&(cross<len(x)-30)]
    cross=cross+(mid-smooth[cross])/(smooth[cross+1]-smooth[cross])
    period=float(np.median(np.diff(cross)))
    cycle=np.rint((cross-cross[0])/period)
    period,origin=np.polyfit(cycle,cross,1)
    phase=(n-origin)/period;phase=(phase+.5)%1-.5
    down=np.where(phase>0,phase-.5,phase+.5)
    upmask=np.abs(phase)<.18;dnmask=np.abs(down)<.18
    rows=[];pred=np.zeros(len(x));valid=upmask|dnmask
    for k in range(5):
        u=upmask&(n%5==k);d=dnmask&(n%5==k)
        mu,bu=np.polyfit(phase[u],x[u],1);md,bd=np.polyfit(down[d],x[d],1)
        pred[u]=mu*phase[u]+bu;pred[d]=md*down[d]+bd
        rows.append({'slot':k,'rising_slope':float(mu),'falling_slope':float(md),'rising_midpoint':float(bu),'falling_midpoint':float(bd)})
    common={key:np.mean([r[key] for r in rows]) for key in rows[0] if key!='slot'}
    target=np.where(upmask,common['rising_slope']*phase+common['rising_midpoint'],common['falling_slope']*down+common['falling_midpoint'])
    for r in rows:
        r['gain']=(common['rising_slope']-common['falling_slope'])/(r['rising_slope']-r['falling_slope'])
        r['offset_codes']=(common['rising_midpoint']+common['falling_midpoint']-r['gain']*(r['rising_midpoint']+r['falling_midpoint']))/2
        r['relative_skew_ns']=((r['gain']*(r['rising_midpoint']-r['falling_midpoint'])-(common['rising_midpoint']-common['falling_midpoint']))/(common['rising_slope']-common['falling_slope']))*period/fs*1e9
    return {'frequency_hz':float(fs/period),'period_samples':float(period),'min_code':int(x.min()),'max_code':int(x.max()),'rail_fraction':float(np.mean((x<=1)|(x>=254))),'raw_segment_residual_rms_codes':float(np.std((x-target)[valid])),'per_core_fit_residual_rms_codes':float(np.std((x-pred)[valid])),'cores':rows}, (phase,down,upmask,valid,common)


def validate(x, calibration):
    result,(phase,down,upmask,valid,common)=fit(x)
    n=np.arange(len(x));target=np.where(upmask,common['rising_slope']*phase+common['rising_midpoint'],common['falling_slope']*down+common['falling_midpoint'])
    slope=np.where(upmask,common['rising_slope'],common['falling_slope'])
    # Fit/validation captures use identical start geometry. Do not rotate to
    # improve validation: a changed phase must be surfaced, not hidden.
    corrected=x.astype(float)
    for k,c in enumerate(calibration['cores']):
        m=n%5==k
        corrected[m]=x[m]*c['gain']+c['offset_codes']-slope[m]*c['relative_skew_ns']*1e-9*result['frequency_hz']
    result['held_out_corrected_segment_residual_rms_codes']=float(np.std((corrected-target)[valid]))
    return result


def main():
    p=argparse.ArgumentParser();p.add_argument('records',nargs='+',type=pathlib.Path);p.add_argument('--out',type=pathlib.Path,required=True);a=p.parse_args()
    records=[];calibration=None
    for path in a.records:
        data=path.read_bytes();assert len(data)%2==0
        x=np.frombuffer(data,dtype=np.uint8).reshape(-1,2)
        if calibration is None:
            calibration=[fit(x[:,ch])[0] for ch in range(2)]
            channels=calibration
        else:channels=[validate(x[:,ch],calibration[ch]) for ch in range(2)]
        records.append({'file':str(path),'sha256':hashlib.sha256(data).hexdigest(),'channels':channels})
    result={'sample_rate_hz':500000000,'fit_record':str(a.records[0]),'records':records,'limits':['Relative calibration at this analog range and temperature only.','1 MHz triangle slope fit does not qualify high-frequency aperture correction or sine-wave ENOB.','Apply coefficients only with known record-start core phase.']}
    a.out.write_text(json.dumps(result,indent=2)+'\n')
    for r in records:
        print(r['file'],[(round(c['frequency_hz'],2),round(c['raw_segment_residual_rms_codes'],3),round(c.get('held_out_corrected_segment_residual_rms_codes',c['per_core_fit_residual_rms_codes']),3)) for c in r['channels']])
if __name__=='__main__':main()
