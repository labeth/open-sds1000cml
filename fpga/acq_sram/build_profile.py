#!/usr/bin/env python3
"""Physical-pin profile fit diagnostic. Never assembles/deploys an unqualified image."""
from pathlib import Path
import argparse
import fcntl
import hashlib
import json
import re
import shutil
import subprocess

parser = argparse.ArgumentParser()
parser.add_argument('--seed', type=int, default=2)
parser.add_argument('--prepare-only', action='store_true')
args = parser.parse_args()
if args.seed < 1:
    raise SystemExit('seed must be positive')
root = Path(__file__).resolve().parent
out = root / 'out' / f'profile-board-seed{args.seed}'
names = ('profile_top.v','profile_pll.v','profile_core.v','command_port.v','command_bridge.v',
         'host_read_port.v','host_read_window.v','acquisition_path.v','acquisition_source.v',
         'acquisition_adc_pll.v','finite_writer.v','record.v','board_capture_path.v',
         'capture_engine.v','finite_recall.v','stream_engine.v','ingress_path.v','ingress_fifo.v',
         'ingress_stream.v','word_bridge.v','timeslice_controller.v','ordinal_counter.v',
         'transport.v','host_path.v','host_packer.v','host_fifo.v','host_sink.v','host_ram.v',
         'host_ownership.v','interleave.v','precision.v','precision_tail.v',
         '../common/gpmc_slave.v','../common/ddio_pair.v','../common/lane_in.v')
with open('/tmp/open-sds-quartus.lock', 'w') as lock:
    fcntl.flock(lock, fcntl.LOCK_EX)
    out.mkdir(parents=True, exist_ok=True)
    for directory in ('db','incremental_db','output_files'):
        shutil.rmtree(out / directory, ignore_errors=True)
    (out / 'result.json').unlink(missing_ok=True)
    hashes = {}
    for name in (*names, '../default/lanemap_seed.vh'):
        data = (root / name).read_bytes()
        (out / Path(name).name).write_bytes(data)
        hashes[name] = hashlib.sha256(data).hexdigest()
    build_id = hashlib.sha256(json.dumps(hashes, sort_keys=True).encode()).hexdigest()[:8]
    pins = {}
    for ball, port in re.findall(r'set_location_assignment PIN_(\w+) -to (\S+)',
                                (root.parent / 'default/default.qsf').read_text()):
        if port.startswith(('gpmc_d[','sel[','enc_p[','enc_n[','lane[')) or port in (
                'clk','mclk_in','nCS1','nOE','nWE','gpmc_a2','gpmc_b1',
                'k1','k2','g1','g2','d1','d2','f1','f2','j2','a11'):
            pins[port] = ball
    for index, ball in enumerate('J6 F5 L2 L1 L3 N2 N1 K5 L4 R1 P2 P1 F3 G5 N3 P3 N5 N6 D3 M6 R5 T5 R6 T6 R3 R7 T7 T3 T2 R4 T4 F7'.split()):
        pins[f'dq[{index}]'] = ball
    expected = {f'{name}[{bit}]' for name, bits in (
        ('gpmc_d',range(16)),('sel',range(2,7)),('enc_p',range(5)),
        ('enc_n',range(5)),('lane',range(80)),('dq',range(32))) for bit in bits}
    expected.update(('clk','mclk_in','nCS1','nOE','nWE','gpmc_a2','gpmc_b1',
                     'k1','k2','g1','g2','d1','d2','f1','f2','j2','a11'))
    assert set(pins) == expected, (expected - set(pins), set(pins) - expected)
    assert len(set(pins.values())) == len(pins), 'pin collision'
    qsf = ['set_global_assignment -name FAMILY "Cyclone IV E"',
           'set_global_assignment -name DEVICE EP4CE10F17C8',
           'set_global_assignment -name TOP_LEVEL_ENTITY acq_profile_top',
           'set_global_assignment -name PROJECT_OUTPUT_DIRECTORY output_files',
           'set_global_assignment -name NUM_PARALLEL_PROCESSORS 2',
           'set_global_assignment -name OPTIMIZATION_MODE BALANCED',
           'set_global_assignment -name RESERVE_ALL_UNUSED_PINS "AS INPUT TRI-STATED"',
           'set_global_assignment -name RESERVE_FLASH_NCE_AFTER_CONFIGURATION "USE AS REGULAR IO"',
           'set_global_assignment -name CYCLONEII_RESERVE_NCEO_AFTER_CONFIGURATION "USE AS REGULAR IO"',
           'set_global_assignment -name ON_CHIP_BITSTREAM_DECOMPRESSION OFF',
           'set_global_assignment -name AUTO_SHIFT_REGISTER_RECOGNITION OFF',
           'set_global_assignment -name SDC_FILE profile.sdc',
           f'set_global_assignment -name SEED {args.seed}',
           'set_parameter -name ENABLE_STREAM 0',
           f'set_parameter -name BUILD_ID "32\'h{build_id}"']
    qsf += [f'set_global_assignment -name VERILOG_FILE {Path(name).name}' for name in names]
    for port, ball in pins.items():
        qsf += [f'set_location_assignment PIN_{ball} -to {port}',
                f'set_instance_assignment -name IO_STANDARD "3.3-V LVTTL" -to {port}']
        if port.startswith(('dq[','enc_')) or port in ('k1','g1','g2','d1','f1','f2','j2','a11'):
            qsf.append(f'set_instance_assignment -name CURRENT_STRENGTH_NEW "MINIMUM CURRENT" -to {port}')
    qsf += [f'set_instance_assignment -name CURRENT_STRENGTH_NEW 8MA -to dq[{bit}]' for bit in range(32)]
    qsf += ['set_instance_assignment -name FAST_INPUT_REGISTER ON -to dq[*]',
            'set_instance_assignment -name FAST_OUTPUT_REGISTER ON -to dq[*]',
            'set_instance_assignment -name FAST_INPUT_REGISTER ON -to lane[*]']
    (out / 'profile.qsf').write_text('\n'.join(qsf)+'\n')
    (out / 'profile.qpf').write_text('PROJECT_REVISION = "profile"\n')
    # No blanket asynchronous clock groups or ADC/SRAM pad false paths.
    # Pad budgets are not invented: this first fit explicitly remains incomplete.
    sdc = '''create_clock -name mref -period 10.0 [get_ports mclk_in]
create_clock -name host -period 10.0 [get_ports clk]
derive_pll_clocks
derive_clock_uncertainty
set enable_stream 0
source stream_path_cdc.sdc
source acquisition_config_cdc.sdc
# Existing board GPMC combinational access budget; pad delays remain unqualified.
set_max_delay 30.0 -from [get_ports {nCS1 nOE sel[*] gpmc_a2 gpmc_b1}] -to [get_ports {gpmc_d[*]}]
# Held command/status bundles cross behind synchronized request tokens.
foreach instance {bridge response} {
 set held [get_registers "*commands|${instance}|held_payload*"]
 set sampled [get_registers "*commands|${instance}|core_payload*"]
 if {[get_collection_size $held]==0 || [get_collection_size $sampled]==0} {error "Missing command mailbox payload"}
 set_max_delay 6.0 -from $held -to $sampled
 set_min_delay 0.0 -from $held -to $sampled
 foreach name {request_sync ack_sync} {
  set first [get_registers [format {*commands|%s|%s[0]} $instance $name]]
  if {[get_collection_size $first]!=1} {error "Missing command mailbox synchronizer"}
  set_max_delay 4.0 -to $first
  set_min_delay 0.0 -to $first
 }
}
set fault_first [get_registers {*profile|acquisition_fault_host[0]}]
if {[get_collection_size $fault_first]!=1} {error "Missing profile fault synchronizer"}
set_max_delay 4.0 -to $fault_first
set_min_delay 0.0 -to $fault_first
'''
    (out / 'profile.sdc').write_text(sdc)
    for name in ('stream_path_cdc.sdc','acquisition_config_cdc.sdc'):
        data = (root / name).read_text()
        if name == 'stream_path_cdc.sdc':
            data = data.replace('set_false_path -from [get_ports reset]',
                                '# Internal board reset remains timed in this diagnostic.')
        (out / name).write_text(data)
        hashes[name] = hashlib.sha256(data.encode()).hexdigest()
    for name in ('profile.qsf','profile.sdc'):
        hashes[name] = hashlib.sha256((out / name).read_bytes()).hexdigest()
    manifest = {'qualification':'physical-pin diagnostic; ADC/SRAM/GPMC pad timing, reset and full CDC audit incomplete; no assembly',
                'build_id':build_id,'seed':args.seed,'sources':hashes,'pins':pins}
    (out / 'manifest.json').write_text(json.dumps(manifest,indent=2)+'\n')
    shutil.copy2(root / 'sim/ddr_model.v', out / 'ddr_model.v')
    subprocess.run(['iverilog','-g2012','-DSIM','-i','-I',str(out),'-s','acq_profile_top',
                    '-o',str(out/'preflight.vvp'),*[str(out/Path(name).name) for name in names],
                    str(out/'ddr_model.v')],check=True)
    print(f'Interface/pin preflight passed: {len(pins)} unique pins; vendor primitives unresolved',flush=True)
    if args.prepare_only:
        raise SystemExit(0)
    quartus = Path('/home/labeth/intelFPGA_lite/21.1/quartus/bin')
    for tool in ('quartus_map','quartus_fit','quartus_sta'):
        print(tool,flush=True)
        with (out/(tool+'.log')).open('w') as log:
            result = subprocess.run([str(quartus/tool),'profile'],cwd=out,stdout=log,stderr=subprocess.STDOUT)
        if result.returncode:
            print((out/(tool+'.log')).read_text(errors='replace')[-7000:])
            raise SystemExit(result.returncode)
    fit = (out/'output_files/profile.fit.rpt').read_text(errors='replace')
    sta = (out/'output_files/profile.sta.rpt').read_text(errors='replace')
    expected_clocks = {f'clocks|pll|auto_generated|pll1|clk[{i}]':values
                       for i,values in enumerate(((4.0,0.0),(4.0,1.0),(8.0,0.0)))}
    expected_clocks.update({f'profile|acquisition|source|frontend|clocks|p|auto_generated|pll1|clk[{i}]':(10.0,phase)
                            for i,phase in enumerate((3.0,4.0,2.0,0.0,1.0))})
    for clock,(period,rise) in expected_clocks.items():
        entry = re.search(r'; '+re.escape(clock)+r'\s*; Generated\s*;\s*([0-9.]+)\s*;[^;]+;\s*([0-9.]+)\s*;',sta)
        if not entry or tuple(map(float,entry.groups())) != (period,rise):
            raise RuntimeError(f'Unexpected derived clock: {clock}')
    timing = re.search(r'; Worst-case Slack\s*;\s*([-.0-9]+)\s*;\s*([-.0-9]+)\s*;([^;\n]*);([^;\n]*);\s*([-.0-9]+)\s*;',sta)
    if not timing:
        raise RuntimeError('missing timing summary')
    result = {'qualification':manifest['qualification'],'build_id':build_id,'derived_clocks_checked':len(expected_clocks),
              'timing':{name:None if value.strip()=='N/A' else float(value)
                        for name,value in zip(('setup','hold','recovery','removal','minimum_pulse'),timing.groups())}}
    for name,label in (('logic_elements','Total logic elements'),('registers','Total registers'),
                       ('labs','Total LABs:  partially or completely used'),('m9k','M9Ks')):
        match = re.search(r'; '+re.escape(label)+r'\s*;\s*([0-9,]+)',fit)
        result[name] = int(match.group(1).replace(',','')) if match else None
    (out/'result.json').write_text(json.dumps(result,indent=2)+'\n')
    print(json.dumps(result,indent=2),flush=True)
    print('Physical fit complete; inspect reports. No deployable image generated.',flush=True)
