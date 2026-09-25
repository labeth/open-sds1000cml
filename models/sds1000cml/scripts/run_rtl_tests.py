#!/usr/bin/env python3
"""Run reviewed, offline Icarus testbenches; preserve commands, hashes and outputs."""
import hashlib
import json
from pathlib import Path
import subprocess
import tempfile
from inventory import ROOT, SOURCE, REV

ACQ = 'fpga/acq_sram/'
CASES = [
    dict(id='ordinal-counter', test=ACQ+'sim/tb_ordinal_counter.v', top='tb', sources=[ACQ+'ordinal_counter.v'], requirements=['REQ-SDS-047'], success='PASS 64-bit ordinals across all carry boundaries and stalled steps'),
    dict(id='host-ram', test=ACQ+'sim/tb_host_ram.v', top='tb', sources=[ACQ+'host_ram.v'], requirements=['REQ-SDS-051'], success='PASS host RAM:'),
    dict(id='host-read-window', test=ACQ+'sim/tb_host_read_window.v', top='tb_host_read_window', sources=[ACQ+'host_read_window.v', ACQ+'host_ram.v', 'fpga/common/gpmc_slave.v'], requirements=['REQ-SDS-053','REQ-SDS-079'], success='PASS host read window:'),
    dict(id='host-read-port', test=ACQ+'sim/tb_host_read_port.v', top='tb_host_read_port', sources=[ACQ+'host_read_port.v', ACQ+'host_read_window.v', ACQ+'host_ram.v', 'fpga/common/gpmc_slave.v'], requirements=['REQ-SDS-053'], success='PASS host read port:'),
]
for source_half, dest_half, phase in [(4,2,0), (2,5,1), (5,2,1), (3,3,2)]:
    CASES.append(dict(id=f'word-bridge-{source_half}-{dest_half}-{phase}', test=ACQ+'sim/tb_word_bridge.v', top='tb', sources=[ACQ+'word_bridge.v'],
                      requirements=['REQ-SDS-093'], success='PASS word bridge delivered=',
                      parameters=dict(SOURCE_HALF=source_half, DEST_HALF=dest_half, PHASE=phase)))


HOST_PATH_SOURCES = [ACQ + name + '.v' for name in
                     ['host_path','host_packer','host_fifo','host_sink','host_ram','host_ownership']]
for phase in [1,2,3]:
    for sync_stall in [0,1]:
        CASES.append(dict(id=f'host-fifo-{phase}-{sync_stall}', test=ACQ+'sim/tb_host_fifo.v', top='tb',
                          sources=[ACQ+'host_fifo.v'], requirements=['REQ-SDS-050','REQ-SDS-078'], success='PASS host FIFO',
                          parameters=dict(PHASE=phase,SYNC_STALL=sync_stall)))
CASES.extend([
    dict(id='host-ownership', test=ACQ+'sim/tb_host_ownership.v', top='tb_host_ownership', sources=[ACQ+'host_ownership.v'], requirements=['REQ-SDS-052'], success='PASS ownership:'),
    dict(id='host-sink', test=ACQ+'sim/tb_host_sink.v', top='tb_host_sink', sources=[ACQ+'host_sink.v',ACQ+'host_ram.v'], requirements=['REQ-SDS-094'], success='PASS host sink:'),
])
for phase in [0,1,2,3]:
    CASES.append(dict(id=f'host-path-{phase}', test=ACQ+'sim/tb_host_path.v', top='tb_host_path', sources=HOST_PATH_SOURCES,
                      requirements=['REQ-SDS-049','REQ-SDS-050','REQ-SDS-051','REQ-SDS-052','REQ-SDS-094'], success='PASS host path', parameters=dict(PHASE=phase)))
    CASES.append(dict(id=f'host-faults-{phase}', test=ACQ+'sim/tb_host_faults.v', top='tb_host_faults', sources=HOST_PATH_SOURCES,
                      requirements=['REQ-SDS-094'], success='PASS host faults', parameters=dict(PHASE=phase)))


# REQ-SDS-046: loss detection and lossless buffering before a reported fault.
for banks, rows in [(1, 16), (2, 16), (4, 16), (9, 512)]:
    CASES.append(dict(id=f'ingress-fifo-{banks}-{rows}', test=ACQ+'sim/tb_ingress_fifo.v', top='tb',
                      sources=[ACQ+'ingress_fifo.v'], requirements=['REQ-SDS-046'], success='PASS ingress banks=',
                      parameters=dict(BANKS=banks, ROWS=rows)))
    CASES.append(dict(id=f'ingress-stream-{banks}-{rows}', test=ACQ+'sim/tb_ingress_stream.v', top='tb',
                      sources=[ACQ+'ingress_stream.v', ACQ+'ingress_fifo.v'], requirements=['REQ-SDS-046'], success='PASS ingress stream depth=',
                      parameters=dict(BANKS=banks, ROWS=rows)))
for phase in [0, 1, 2, 3]:
    CASES.append(dict(id=f'ingress-path-{phase}', test=ACQ+'sim/tb_ingress_path.v', top='tb',
                      sources=[ACQ+'ingress_path.v', ACQ+'ingress_stream.v', ACQ+'ingress_fifo.v', ACQ+'word_bridge.v'],
                      requirements=['REQ-SDS-046'], success='PASS ingress path RAM overflow detected', parameters=dict(PHASE=phase)))


CASES.append(dict(id='transport-full-capacity', test=ACQ+'sim/tb_transport.v', top='tb',
                  sources=[ACQ+'transport.v',ACQ+'sim/ddr_model.v'], requirements=['REQ-SDS-044'],
                  success='PASS full-capacity SRAM transport', timeoutSeconds=180))
SCHEDULER_SOURCES = [ACQ+name+'.v' for name in ['timeslice_controller','transport','ordinal_counter',
                      'word_bridge','ingress_fifo','ingress_stream','ingress_path']] + [ACQ+'sim/ddr_model.v'] + HOST_PATH_SOURCES
for label, params, success in [
    ('reset-writing', dict(RESET_PHASE=1), 'PASS active reset'),
    ('reset-reading', dict(RESET_PHASE=2), 'PASS active reset'),
    ('consecutive-captures', dict(AW=8,BANK_WORDS=2,TARGET=201,STALL_CYCLES=0,EPOCHS=2), 'PASS RTL controller'),
    ('wrap-tail-stall', {}, 'PASS RTL controller'),
    ('steady-bandwidth', dict(STALL_CYCLES=0), 'PASS RTL controller'),
    ('counter-lanes', dict(AW=12,BANK_WORDS=640,TARGET=10003,STALL_CYCLES=0), 'PASS RTL controller'),
    ('one-word-tail', dict(AW=8,BANK_WORDS=2,TARGET=201,STALL_CYCLES=0), 'PASS RTL controller'),
    ('unread-protection', dict(NO_RELEASE=1), 'PASS controller unread protection'),
    ('owned-host-stall', dict(REAL_HOST=1,AW=13,BANK_WORDS=2560,TARGET=10003,STALL_BLOCK=0,STALL_CYCLES=250000,REQUIRE_STALL=1), 'PASS RTL controller'),
    ('physical-geometry', dict(AW=19,BANK_WORDS=2560,TARGET=65539,STALL_CYCLES=2500000), 'PASS RTL controller'),
]:
    CASES.append(dict(id='scheduler-'+label,test=ACQ+'sim/tb_timeslice_controller.v',top='tb',sources=SCHEDULER_SOURCES,
                      requirements=['REQ-SDS-048'],success=success,parameters=params,timeoutSeconds=900))


def snapshot_sources(source, destination, cases):
    """Freeze all compilation inputs before the first compiler runs."""
    hashes = {}
    for name in sorted({p for case in cases for p in [case['test'], *case['sources']]}):
        relative = Path(name)
        if relative.is_absolute() or '..' in relative.parts:
            raise ValueError(f'non-local RTL input: {name}')
        data = (source / relative).read_bytes()
        target = destination / relative
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(data)
        hashes[name] = hashlib.sha256(data).hexdigest()
    return hashes


def run():
    cases = []
    with tempfile.TemporaryDirectory(prefix='sds-rtl-tests-') as tmp:
        snapshot = Path(tmp) / 'sources'
        hashes = snapshot_sources(SOURCE, snapshot, CASES)
        for case in CASES:
            files = [case['test'], *case['sources']]
            binary = str(Path(tmp) / case['id'])
            command = ['iverilog', '-g2012', '-s', case['top'], '-o', binary]
            command += [f'-P{case["top"]}.{key}={value}' for key, value in case.get('parameters', {}).items()]
            command += files
            compile_result = subprocess.run(command, cwd=snapshot, text=True, capture_output=True, timeout=60)
            simulation = subprocess.run(['vvp', binary], cwd=snapshot, text=True, capture_output=True, timeout=case.get('timeoutSeconds', 60)) if compile_result.returncode == 0 else None
            passed = (simulation is not None and simulation.returncode == 0 and
                      any(line.startswith(case['success']) for line in simulation.stdout.splitlines()))
            cases.append(dict(**case, sha256={p:hashes[p] for p in files}, sourceSnapshot=True, workingDirectory=str(snapshot),
                              compileCommand=command, compileExitCode=compile_result.returncode,
                              compileOutput=compile_result.stdout+compile_result.stderr,
                              simulationCommand=['vvp', binary], simulationExitCode=simulation.returncode if simulation else None,
                              simulationOutput=simulation.stdout+simulation.stderr if simulation else '', result='pass' if passed else 'fail'))
            print(case['id'], cases[-1]['result'], flush=True)
    report = dict(commit=REV, scope='Offline Icarus simulations with ideal digital clocks and behavioral RAM (ALTERA_RESERVED_QIS is not defined). No fitted primitive simulation, metastability analysis, physical timing or board qualification.',
                  cases=cases, result='pass' if all(c['result']=='pass' for c in cases) else 'fail')
    target=ROOT/'evidence/test-runs/rtl-reviewed.json'
    target.write_text(json.dumps(report,indent=2)+'\n')
    index=ROOT/'evidence/test-runs/results.json'
    rows=[r for r in json.loads(index.read_text()) if r['id']!='rtl-reviewed']
    rows.append(dict(id='rtl-reviewed', repository='open-sds1000cml-acq2', commit=REV, workingDirectory='.',
                     command='python3 models/sds1000cml/scripts/run_rtl_tests.py', exitCode=0 if report['result']=='pass' else 1,
                     result=report['result'], log=target.name, sha256=hashlib.sha256(target.read_bytes()).hexdigest(), scope=report['scope']))
    index.write_text(json.dumps(rows,indent=2)+'\n')
    assert report['result']=='pass', 'RTL simulation failure; inspect preserved output'


if __name__ == '__main__':
    run()
