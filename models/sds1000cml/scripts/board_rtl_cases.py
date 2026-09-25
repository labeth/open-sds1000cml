"""Reviewed board build variants; simulation models do not establish board qualification."""
# ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
# TRLC-LINKS: REQ-SDS-059
A='fpga/acq_sram/'
BASE=[A+'top.v',A+'record.v',A+'transport.v','fpga/common/gpmc_slave.v']
VENDOR_DIR='/home/labeth/intelFPGA_lite/21.1/quartus/eda/sim_lib'
VENDOR_FILES=['altera_mf.v','220model.v','altera_primitives.v']
CASES=[]
def case(name,label,flags,sources,success=None,plusargs=None,vendor=False,oracle=None):
    CASES.append(dict(id=label,test=A+'sim/tb_'+name+'.v',top='tb',sources=sources,defines=['SIM',*flags],plusargs=plusargs or [],vendor=vendor,success=success,oracle=oracle,timeoutSeconds=900))
base=BASE+[A+'sim/ddr_model.v']
case('top','legacy-base',[],base+[A+'adc_unpack.v','fpga/common/lane_in.v','fpga/common/ddio_pair.v'],'PASS integrated full-depth capture')
case('top_interleave','legacy-interleave',['INTERLEAVE'],base,'PASS interleave memory path:')
case('burst','legacy-burst',['INTERLEAVE','BURST_RECALL'],base,'PASS 4096-word read buffer')
case('gpmc_read','qualified-gpmc-read',[],['fpga/common/gpmc_slave.v'],'PASS qualified reads')
case('gpmc_burst','legacy-gpmc-burst',['INTERLEAVE','BURST_RECALL','HOST_READ_FIX'],base,'PASS full GPMC read path:')
case('continuation','legacy-continuation',['INTERLEAVE','BURST_RECALL'],base,oracle='exact-continuation-values')
case('precision_top','legacy-precision',['INTERLEAVE','BURST_RECALL','PRECISION','HOST_READ_FIX'],BASE+[A+'precision.v'],'PASS sparse /256:',vendor=True)
for aw in [12,13]:
    case('stream_top','legacy-stream-'+str(1<<aw),['INTERLEAVE','BURST_RECALL','PRECISION','HOST_READ_FIX','STREAM_CAPTURE','STREAM_BUFFER_AW='+str(aw)],base+[A+'stream_packetizer.v',A+'stream_banks.v'],'PASS integrated stream tap:')
for plus in [[],['+normal'],['+recall'],['+normal','+recall']]:
    case('top_adc','legacy-adc'+''.join('-'+x[1:] for x in plus),['INTERLEAVE'],BASE+[A+'interleave.v','fpga/common/lane_in.v','fpga/common/ddio_pair.v'],'PASS integrated ADC to SRAM:' if '+recall' in plus else 'PASS integrated ADC capture:',plusargs=plus,vendor=True)

def outcome(case):
    import re
    if case['compileExitCode']!=0:
        assert case['simulationExitCode'] is None and not case['simulationOutput'] and not case['timedOut']
        return 'not-run'
    assert case['simulationExitCode'] is not None
    if case['timedOut'] or case['simulationExitCode']!=0:return 'fail'
    if case['oracle']=='exact-continuation-values':
        actual=[tuple(map(int,m)) for m in re.findall(r'^continue start=(\d+) index=(\d+) value=(\d+)$',case['simulationOutput'],re.M)]
        expected=[(start,i,start+i) for start,n in [(32,1),(33,4),(37,16),(53,16)] for i in range(n)]
        passed=actual==expected
    else:
        passed=any(line.startswith(case['success']) for line in case['simulationOutput'].splitlines())
    return 'partial' if passed else 'fail'
