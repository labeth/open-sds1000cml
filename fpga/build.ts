// ENGMODEL-OWNER-UNIT: FU-FPGA-BUILD
// Builds one FPGA image for the EP4CE10F17C8 with headless Quartus 21.1 Lite:
//
//   node --experimental-strip-types fpga/build.ts --image=general|packet|line|stacking|stream [--seed=N] [--prepare-only] [--install]
//
// Every image is common/ (the acquisition top, SRAM record and transport, ADC
// interleave, panel scan, clocks and GPMC) plus its own decoders or engine:
// general, packet and line add trigger/ (decoded events, sequence trigger,
// envelope recall) and their protocol triggers; stacking adds the stacking
// engine. The default seeds are the shipping images' (bitstreams/images.json).
// A build that fails timing or its checks never publishes. --install copies a
// passing build into bitstreams/ and records it in the manifest.
import {readFileSync,writeFileSync,copyFileSync,mkdirSync,mkdtempSync,openSync,closeSync,statSync,symlinkSync,renameSync,existsSync} from 'node:fs';
import {dirname,join,basename} from 'node:path';
import {fileURLToPath} from 'node:url';
import {spawnSync} from 'node:child_process';
import {createHash} from 'node:crypto';

const root=dirname(fileURLToPath(import.meta.url));
const args=process.argv.slice(2);
const flag=(f:string)=>args.includes(f);
const value=(k:string)=>args.find(a=>a.startsWith(k+'='))?.slice(k.length+1);
if(args.some(a=>!['--prepare-only','--install'].includes(a) && !/^--image=\w+$/.test(a) && !/^--seed=\d+$/.test(a)))
 throw Error('usage: build.ts --image=general|packet|line|stacking|stream [--seed=N] [--prepare-only] [--install]');

type Image={dir:string,files:string[],defines:string[],seed:number,trigger:boolean,precision:boolean,bare?:boolean};
// Shared by every image (the stacking build used the same PLL text).
const common=['record.v','transport.v','adc_unpack.v','interleave.v','panel_scan.v','pll.v','adc_pll.v',
 'gpmc_slave.v','ddio_pair.v','lane_in.v','lanemap_seed.vh'];
// Shared by the three trigger images.
const trigger=['sample_timeline.v','event_sequence.v','envelope_reduce.v','decoded_event_queue.v','decoded_event_bridge.v',
 'decoded_event_reader.v','decoded_event_transport.v','decoded_event_port.v'];
const images:Record<string,Image>={
 general:{dir:'images/general',files:['uart_trigger.v','i2c_trigger.v','spi_trigger.v','precision.v','precision_tail.v'],
  defines:['TRIG_UART','TRIG_I2C','TRIG_SPI'],seed:1,trigger:true,precision:true},
 // Line held Manchester, MIL-1553, USB and SENT at 94% and missed timing on
 // four seeds; MIL-1553 and SENT moved to the packet image.
 packet:{dir:'images/packet',files:['arinc429_trigger.v','can_trigger.v','flexray_trigger.v','mil1553_trigger.v','sent_trigger.v'],
  defines:['TRIG_ARINC','TRIG_CAN','TRIG_FLEXRAY','TRIG_MIL','TRIG_SENT','SEQUENCE_ONLY'],seed:6,trigger:true,precision:false},
 line:{dir:'images/line',files:['manchester_receive.v','manchester_trigger.v','usbls_trigger.v','protocol_scratch.v'],
  defines:['TRIG_MAN','TRIG_USB','SEQUENCE_ONLY'],seed:7,trigger:true,precision:false},
 // ADR-STREAM-IMAGE: continuous capture for roll and loss-free streaming; no
 // protocol decoders, so the packetizer, banks and precision decimation fit.
 // STREAM_DRUM: the stream goes through the external SRAM as a FIFO, whose
 // 512K words are the drain's slack (stream_drum.v).
 stream:{dir:'images/stream',files:['stream_packetizer.v','stream_banks.v','stream_peak.v','stream_drum.v','general/precision.v','general/precision_tail.v'],defines:['STREAM_CAPTURE','STREAM_DRUM'],seed:32,trigger:true,precision:true,bare:true},
 stacking:{dir:'images/stacking',files:['stack_engine_port.v','stack_edge_accumulator.v','stack_edge_hits.v','stack_tiled_accumulator.v',
  'stack_tile_access.v','stack_tile_transfer.v','stack_state_tile.v','stack_resample_store.v','stack_resample.v','stack_positions.v',
  'stack_interpolate.v','stack_accumulate.v','stack_bin_writer.v','stack_record_cache.v'],defines:[],seed:11,trigger:false,precision:false},
};
const name=value('--image') ?? 'general';
const image=images[name];if(!image)throw Error('unknown image '+name);
const seed=Number(value('--seed') ?? image.seed);

// One Quartus flow at a time on this host.
if(!flag('--prepare-only') && process.env.SDS_FPGA_BUILD_LOCKED!=='1'){
 const r=spawnSync('flock',['-n','/tmp/open-sds-quartus.lock',process.execPath,...process.execArgv,fileURLToPath(import.meta.url),...args],
  {stdio:'inherit',env:{...process.env,SDS_FPGA_BUILD_LOCKED:'1'}});
 if(r.error)throw r.error;
 process.exit(r.status ?? 1);
}

const products=join(root,'out',name);
mkdirSync(products,{recursive:true});
// A fresh project per build; a failed build never publishes.
const out=mkdtempSync(join(products,'build-'));
const inputs:Record<string,string>={};
const sha=(b:Buffer|string)=>createHash('sha256').update(b).digest('hex');
// TRLC-LINKS: REQ-SDS-210
function copy(source:string,file:string){copyFileSync(source,join(out,file));inputs[file]=sha(readFileSync(source));}
// TRLC-LINKS: REQ-SDS-210
function project(file:string,text:string){writeFileSync(join(out,file),text);inputs[file]=sha(text);}

// An image may name another image's file as dir/file (shared, not copied in the tree).
const imageFile=(f:string)=>f.includes('/') ? basename(f) : f;
const sources=[...(image.trigger ? trigger : []),...image.files.map(imageFile)];
for(const f of common)copy(join(root,'common',f),f);
for(const f of image.trigger ? trigger : [])copy(join(root,'trigger',f),f);
for(const f of image.files)copy(f.includes('/') ? join(root,'images',f) : join(root,image.dir,f),imageFile(f));

let defines:string[];
if(image.trigger){
 // The shared trigger project lists every decoder; keep this image's files only.
 const listed=new Set(['pll.v','adc_pll.v','bench.v','gpmc_slave.v','ddio_pair.v','lane_in.v','record.v','transport.v','adc_unpack.v',
  'interleave.v','panel_scan.v',...sources]);
 let qsf=readFileSync(join(root,'trigger','project.qsf'),'utf8').split('\n')
  .filter(l=>!l.startsWith('set_global_assignment -name VERILOG_FILE ') || listed.has(l.slice('set_global_assignment -name VERILOG_FILE '.length)));
 for(const f of ['record.v','transport.v','adc_unpack.v','interleave.v','sample_timeline.v','event_sequence.v','envelope_reduce.v',
  'decoded_event_queue.v','decoded_event_bridge.v','decoded_event_reader.v','decoded_event_transport.v','decoded_event_port.v','panel_scan.v',
  ...image.files.map(imageFile)])if(!qsf.includes(`set_global_assignment -name VERILOG_FILE ${f}`))qsf.push(`set_global_assignment -name VERILOG_FILE ${f}`);
 qsf=qsf.map(l=>l.startsWith('set_global_assignment -name SEED ') ? `set_global_assignment -name SEED ${seed}` : l);
 qsf=qsf.map(l=>l==='set_global_assignment -name FITTER_EFFORT "STANDARD FIT"' ? 'set_global_assignment -name FITTER_EFFORT "AUTO FIT"' : l);
 // Placement and routing effort, but no extra physical synthesis, whose
 // duplicated registers fill the device.
 qsf.push('set_global_assignment -name ROUTER_TIMING_OPTIMIZATION_LEVEL MAXIMUM','set_global_assignment -name PLACEMENT_EFFORT_MULTIPLIER 4');
 project('bench.qsf',qsf.join('\n'));
 const sdc=readFileSync(join(root,'trigger','project.sdc'),'utf8');
 // ADR-STREAM-IMAGE: the stream crosses 250 -> 125 MHz through a gray-coded
 // FIFO; only first synchronizer stages are cut, and its storage, stable for
 // clocks before the read pointer reaches it, is excused from the 4 ns path.
 const streamSdc=name==='stream' ? ['',
  'set_false_path -to [get_registers {*stream_pack*reset_s[0] *stream_pack*wgray_s0[*] *stream_pack*rgray_s0[*] *stream_pack*stop_s[0] *stream_pack*fault_s[0]}]',
  'set_false_path -to [get_registers {*stream_queue*preset_s[0] *stream_queue*rel_s0[*] *stream_queue*pub_s0[*] *stream_queue*hreset_s[0] *stream_queue*overrun_s[0]}]',
  // stream_drum's register FIFOs between clk and hclk: an entry is written a
  // clock or more before its pointer shows it on the other side.
  'set_false_path -from [get_registers {*drum|iq~* *drum|rq~*}] -to [get_registers {*drum|wf_in[*] *drum|in_lo[*] *drum|packet_data[*]}]',
  'set_false_path -from [get_registers {*drum|wq~*}] -to [get_registers {*drum|w_data[*]}]',
  'set_false_path -from [get_registers {*stream_pack*mem*}] -to [get_registers {*stream_pack*low[*] *stream_pack*packet_data[*]}]',
  // stream_peak's bucket stage updates at most every other clock from pair
  // registers held two clocks (its design), so those paths get two clocks.
  // The bucket size changes only while acquisition is idle.
  'set_false_path -from [get_registers {*peak_log[*] *peak|size_r[*]}] -to [get_registers {*peak|size_r[*] *peak|init[*]}]',
  'set_multicycle_path -setup -end 2 -from [get_registers {*peak|pmin?[*] *peak|pmax?[*] *peak|min?[*] *peak|max?[*] *peak|left[*] *peak|first}] -to [get_registers {*peak|min?[*] *peak|max?[*] *peak|left[*] *peak|first}]',
  'set_multicycle_path -hold -end 1 -from [get_registers {*peak|pmin?[*] *peak|pmax?[*] *peak|min?[*] *peak|max?[*] *peak|left[*] *peak|first}] -to [get_registers {*peak|min?[*] *peak|max?[*] *peak|left[*] *peak|first}]'].join('\n') : '';
 project('bench.sdc',(image.precision ? sdc : sdc.split('\n').filter(l=>!/precision/i.test(l)).join('\n'))+streamSdc);
 // A bare image (stream) leaves out the protocol baseline (sequence trigger,
 // protocol scratch): it triggers on nothing and needs the room at 250 MHz.
 defines=[...(image.bare ? [] : ['UART_TRIGGER','PROTOCOL_SET']),...image.defines,'INTERLEAVE','BURST_RECALL',...(image.precision ? ['PRECISION'] : []),'HOST_READ_FIX'];
}else{
 // Stacking: interleave and burst recall foundation plus the stacking port,
 // whose pop-on-read mailbox needs the qualified read strobe (HOST_READ_FIX).
 const qsf=readFileSync(join(root,image.dir,'project.qsf'),'utf8')
  .replace(/^set_global_assignment -name SEED \d+$/m,`set_global_assignment -name SEED ${seed}`);
 project('bench.qsf',qsf);
 project('bench.sdc',readFileSync(join(root,image.dir,'project.sdc'),'utf8'));
 defines=['INTERLEAVE','BURST_RECALL','HOST_READ_FIX','STACK_ENGINE'];
}
const top=readFileSync(join(root,'common','top.v'),'utf8');
writeFileSync(join(out,'bench.v'),defines.map(d=>'`define '+d+'\n').join('')+'`define BENCH_MHZ 250\n'+top);
inputs['bench.v']=sha(readFileSync(join(out,'bench.v')));
writeFileSync(join(out,'bench.qpf'),'PROJECT_REVISION = "bench"\n');
writeFileSync(join(out,'inputs.json'),JSON.stringify(inputs,null,2)+'\n');
console.log(`Prepared ${out}`);
if(flag('--prepare-only'))process.exit(0);

const bin=process.env.QUARTUS_BIN || join(process.env.HOME!,'intelFPGA_lite/21.1/quartus/bin');
// TRLC-LINKS: REQ-SDS-210
function run(tool:string,argv:string[]){
 console.log(tool);
 const fd=openSync(join(out,tool+'.log'),'w');
 try{
  const r=spawnSync(join(bin,tool),argv,{cwd:out,stdio:['ignore',fd,fd]});
  if(r.error || r.status!==0)throw Error(`${tool} failed: ${r.error || r.status}; see ${out}/${tool}.log`);
 }finally{closeSync(fd);}
}
run('quartus_map',['bench']);
run('quartus_fit',['bench']);
run('quartus_sta',['bench']);
const summary=readFileSync(join(out,'output_files','bench.sta.summary'),'utf8');
const slack=[...summary.matchAll(/^Slack\s*:\s*([-+\d.]+)/gm)].map(m=>Number(m[1]));
if(!slack.length || slack.some(s=>!Number.isFinite(s)||s<0))throw Error('Timing failed or summary missing; refusing to publish');
run('quartus_asm',['bench']);
run('quartus_cpf',['-c','-o','bitstream_compression=off','output_files/bench.sof','bench.rbf']);
if(statSync(join(out,'bench.rbf')).size!==368011)throw Error('Unexpected FPGA image size');
if(!image.trigger)auditStacking();
const rbfSha=sha(readFileSync(join(out,'bench.rbf')));
writeFileSync(join(out,'image.json'),JSON.stringify({image:name,seed,sha256:rbfSha,minimumInternalSlackNs:Math.min(...slack),inputs},null,2)+'\n');
// One atomic pointer publishes the image and its provenance together.
const next=join(products,'next-'+basename(out));
symlinkSync(basename(out),next);
renameSync(next,join(products,'current'));
console.log(`Image: ${products}/current/bench.rbf sha256 ${rbfSha}; minimum internal slack ${Math.min(...slack)} ns`);
if(flag('--install')){
 copyFileSync(join(out,'bench.rbf'),join(root,'bitstreams',name+'.rbf'));
 const manifest=join(root,'bitstreams','images.json');
 const m=existsSync(manifest) ? JSON.parse(readFileSync(manifest,'utf8')) : {};
 m[name]={seed,sha256:rbfSha,inputs:sha(JSON.stringify(inputs))};
 writeFileSync(manifest,JSON.stringify(m,null,2)+'\n');
 console.log(`Installed bitstreams/${name}.rbf`);
}

// The stacking image's fit checks: every ADC lane packed in its input
// register, the fitter placed each assigned pin where asked, and on-chip
// memory is exactly the burst recall plus the stacking port's buffers.
// TRLC-LINKS: REQ-SDS-210
function auditStacking(){
 const fit=readFileSync(join(out,'output_files','bench.fit.rpt'),'latin1');
 for(let i=0;i<80;i++)
  if(!fit.split('\n').some(l=>l.includes('q_ioe[') && l.includes('Fast Input Register assignment') && l.includes(`lane[${i}]~input`)))
   throw Error(`ADC lane ${i} not packed in its input register`);
 const qsf=readFileSync(join(out,'bench.qsf'),'utf8');
 const placed=new Map<string,string>();
 for(const l of fit.split('\n')){const c=l.split(';').map(s=>s.trim());if(c.length>6 && ['input','output','bidir'].includes(c[5]))placed.set(c[1],c[4]);}
 for(const [,ball,port] of qsf.matchAll(/set_location_assignment PIN_(\w+) -to (\S+)/g))
  if(placed.get(ball)!==port)throw Error(`pin ${ball}: assigned ${port}, fitter placed ${placed.get(ball)}`);
 const used=Number(/Total memory bits\s*:\s*([\d,]+)/.exec(readFileSync(join(out,'output_files','bench.fit.summary'),'utf8'))![1].replace(/,/g,''));
 // Burst recall 133632; stacking: moment tile 24576, two cache pages 16384,
 // tile mailboxes 2 x 512, the qualifier's 1024 x 8 template RAM.
 const expected=133632+24576+16384+1024+8192;
 if(used!==expected)throw Error(`expected ${expected} memory bits, got ${used}`);
 console.log(`Audit: 80/80 ADC input registers, pins placed, ${used} memory bits`);
}
