#!/usr/bin/env python3
"""Qualify revision 10 host reads; scope files/configuration are volatile only."""
import argparse, datetime, hashlib, json, pathlib, struct, subprocess, time

ROOT = pathlib.Path(__file__).resolve().parents[3]
p = argparse.ArgumentParser()
p.add_argument('build', type=pathlib.Path)
p.add_argument('--helper', type=pathlib.Path, required=True)
args = p.parse_args()
build = args.build.resolve()
subprocess.run(['python3', str(ROOT/'fpga/acq_sram/audit.py'), str(build)], check=True)
out = ROOT/'validation'/'2026-09-13-default'/datetime.datetime.now().strftime('host-read-%H%M%S')
out.mkdir(parents=True)
ctl = [str(ROOT/'ota/dist/otactl'), '-tcp', '192.168.1.209:5900']
events, results = [], []

def call(argv, check=True, timeout=45):
    r = subprocess.run(ctl+list(map(str, argv)), capture_output=True, text=True, timeout=timeout)
    events.append(dict(args=list(map(str, argv)), code=r.returncode, stdout=r.stdout, stderr=r.stderr))
    (out/'commands.json').write_text(json.dumps(events, indent=2)+'\n')
    if check and r.returncode:
        raise RuntimeError(r.stdout+r.stderr)
    return r

def decode(r):
    return json.JSONDecoder().raw_decode(r.stdout[r.stdout.index('{'):])[0]

def power():
    subprocess.run([str(ROOT/'ota/dist/otactl'), '-shelly', '192.168.1.223', 'power', 'cycle'], check=True, timeout=20, capture_output=True)

def wait_agent():
    for _ in range(50):
        try:
            if call(['ping'], check=False, timeout=5).returncode == 0:
                return
        except subprocess.TimeoutExpired:
            pass
        time.sleep(1)
    raise RuntimeError('agent unavailable')

def probe(*argv, timing=None):
    env = ['SCOPE_EDMA=1']
    if timing:
        env += [f'SCOPE_PROFILE_RD_CYCLE={timing[0]}', f'SCOPE_PROFILE_RD_ACCESS={timing[1]}', f'SCOPE_PROFILE_GAP={timing[2]}']
    return decode(call(['exec', 'env', *env, '/dev/acqsram-host', *argv]))

def recall(off, n, timing):
    r = probe('profile-recall', off, n, timing=timing)
    expected = hashlib.sha256(b''.join(i.to_bytes(4, 'little') for i in range(off, off+n))).hexdigest()
    assert r['bytes'] == n*4 and r['sha256'] == expected and r['nonconsecutive_words'] == 0, r
    results.append(dict(offset=off, words=n, timing=timing, result=r))
    (out/'results.json').write_text(json.dumps(results, indent=2)+'\n')
    print('PASS', off, n, timing, r['seconds'], flush=True)

try:
    power(); wait_agent(); call(['app', 'stop'])
    call(['put', args.helper.resolve(), '/dev/acqsram-host'])
    call(['exec', 'chmod', '755', '/dev/acqsram-host'])
    call(['put', build/'bench.rbf', '/dev/acq-host.rbf'])
    assert 'identity=5a52' in call(['exec', '/dev/acqsram-host', 'load', '/dev/acq-host.rbf']).stdout
    print('Loaded host-read candidate', out, flush=True)
    for log in [0, 4, 8, 0]:
        st = probe('capture', 0, 524271, 17, 128, log)['capture']
        assert st['revision'] == 10 and st['locked'] and not st['data_fault'], st
        for timing in [None, (16, 13, 5), (12, 10, 5), (10, 8, 5)]:
            recall(0, 524288, timing)
        for off, n in [(4079, 33), (524287, 1), (0, 524288)]:
            recall(off, n, (10, 8, 5))
    probe('range-both', 8)
    for log in [0, 4]:
        for cfg in [17, 49, 81, 113]:
            probe('capture', cfg, 262144, 262144, 128, log)
            for _ in range(100):
                st = probe('status')['capture']
                if st['frozen'] and st['ready']:
                    break
                time.sleep(.02)
            else:
                raise RuntimeError('triangle edge did not trigger')
            assert st['triggered'] and not st['data_fault'], st
            probe('recall', '/dev/acq-edge.bin', st['trigger_index']-4, 16)
            path = out/f'edge-d{log}-cfg{cfg}.bin'
            call(['get', '/dev/acq-edge.bin', path])
            b = path.read_bytes()
            ch, falling = (cfg >> 6) & 1, bool(cfg & 32)
            if log:
                samples = [struct.unpack_from('<H', b, 4*i+2*ch)[0] >> 8 for i in range(16)]
                candidates = [4]
            else:
                samples = list(b[ch::2])
                candidates = [8, 9]
            def crossing(i):
                return samples[i-1] > 128 >= samples[i] if falling else samples[i-1] < 128 <= samples[i]
            assert any(crossing(i) for i in candidates), (log, cfg, samples)
            results.append(dict(kind='edge', log=log, config=cfg, status=st, samples=samples))
            (out/'results.json').write_text(json.dumps(results, indent=2)+'\n')
            print('PASS ADC trigger alignment', log, cfg, flush=True)
    (out/'build.json').write_text((build/'audit.json').read_text())
    print('QUALIFIED counter host reads and ADC edge alignment; app qualification still required', out, flush=True)
except Exception:
    power()
    raise
