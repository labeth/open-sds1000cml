import json,math,pathlib,numpy as np
import matplotlib
matplotlib.use('Agg')
import matplotlib.pyplot as plt
P=pathlib.Path(__file__).resolve().parent
records=json.loads((P/'measurements.json').read_text()); selected=['raw','r16','r16avg4','r16avg16','r16avg64','r32avg16'];rows=[]
for name in selected:
 r=next(x for x in records if x['name']==name); corrected=[]
 for x in r['samples']:
  n=x['n'];h=min(15,int(.44/r['header']['sample_s']/1e6)); corrected.append(np.array(x['rms_codes'])*math.sqrt(n/(n-(2*h+2))))
 rms=np.median(corrected,axis=0);bits=np.log2(256/(np.sqrt(12)*rms));rows.append((name,rms,bits,len(corrected)))
fig,ax=plt.subplots(figsize=(10,5),layout='constrained');x=np.arange(len(rows))
for ch,color in [(0,'#b18b00'),(1,'#0099b2')]:ax.plot(x,[r[1][ch] for r in rows],'o-',label=f'CH{ch+1}',color=color)
ax.axhline(256/(4096*np.sqrt(12)),color='#777',linestyle='--',label='Ideal 12-bit quantizer RMS over original ADC range')
ax.set_yscale('log');ax.set_xticks(x,['Raw','FPGA /16','/16 + 4 captures','/16 + 16','/16 + 64','/32 + 16']);ax.set_ylabel('Harmonic-fit residual RMS (8-bit ADC codes)');ax.grid(alpha=.2);ax.legend();ax.set_title('Real hardware: periodic-fit residual falls below the 12-bit noise reference\nHarmonic distortion removed; this is NOT SINAD ENOB or accuracy')
fig.savefig(P/'noise-comparison.png',dpi=160)
lines=['| Mode | CH1 / CH2 residual RMS, ADC codes | Residual-equivalent bits CH1 / CH2 | Records |','|---|---:|---:|---:|']
for name,rms,bits,n in rows:lines.append(f'| {name} | {rms[0]:.5f} / {rms[1]:.5f} | {bits[0]:.2f} / {bits[1]:.2f} | {n} |')
(P/'table.md').write_text('\n'.join(lines)+'\n')
print('\n'.join(lines))
