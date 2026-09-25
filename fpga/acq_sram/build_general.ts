// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
// node --experimental-strip-types fpga/acq_sram/build_general.ts [--prepare-only]
// Single general SRAM and protocol acquisition image; no deployment is performed.
import {readFileSync,writeFileSync,copyFileSync,mkdirSync,mkdtempSync,openSync,closeSync,statSync,symlinkSync,renameSync} from 'node:fs';
import {dirname,join,basename} from 'node:path';
import {fileURLToPath} from 'node:url';
import {spawnSync} from 'node:child_process';
import {createHash} from 'node:crypto';
const root=dirname(fileURLToPath(import.meta.url));
const args=process.argv.slice(2);
if(args.some(a=>a!=='--prepare-only'))throw Error('Only --prepare-only is supported');
if(!args.includes('--prepare-only') && process.env.SDS_GENERAL_BUILD_LOCKED!=='1'){
 const result=spawnSync('flock',['-n','/tmp/open-sds-quartus.lock',process.execPath,...process.execArgv,fileURLToPath(import.meta.url),...args],{stdio:'inherit',env:{...process.env,SDS_GENERAL_BUILD_LOCKED:'1'}});
 if(result.error)throw result.error;
 process.exit(result.status ?? 1);
}
const products=join(root,'out','general');
mkdirSync(products,{recursive:true});
// Fresh source snapshot and reports for every build; failed builds never publish.
const out=mkdtempSync(join(products,'build-'));
const inputs:Record<string,string>={};
function copy(source:string,name:string){
 copyFileSync(source,join(out,name));
 inputs[name]=createHash('sha256').update(readFileSync(source)).digest('hex');
}
for(const name of ['bench.qsf','bench.sdc','pll.v','adc_pll.v'])copy(join(root,'protocol',name),name);
for(const name of ['record.v','transport.v','adc_unpack.v','interleave.v','precision.v','precision_tail.v','uart_trigger.v','i2c_trigger.v','spi_trigger.v','sent_trigger.v','mil1553_trigger.v','usbls_trigger.v','manchester_receive.v','manchester_trigger.v','sample_timeline.v'])copy(join(root,name),name);
for(const name of ['decoded_event_queue.v','decoded_event_bridge.v','decoded_event_reader.v','decoded_event_transport.v','decoded_event_port.v','protocol_scratch.v'])copy(join(root,name),name);
for(const name of ['gpmc_slave.v','ddio_pair.v','lane_in.v'])copy(join(root,'..','common',name),name);
copy(join(root,'..','default','lanemap_seed.vh'),'lanemap_seed.vh');
const top=readFileSync(join(root,'top.v'),'utf8');
writeFileSync(join(out,'bench.v'),'`define UART_TRIGGER\n`define INTERLEAVE\n`define BURST_RECALL\n`define PRECISION\n`define HOST_READ_FIX\n`define BENCH_MHZ 250\n'+top);
inputs['bench.v']=createHash('sha256').update(readFileSync(join(out,'bench.v'))).digest('hex');
writeFileSync(join(out,'bench.qpf'),'PROJECT_REVISION = "bench"\n');
writeFileSync(join(out,'inputs.json'),JSON.stringify(inputs,null,2)+'\n');
console.log(`Prepared ${out}`);
if(!args.includes('--prepare-only')){
 const bin=process.env.QUARTUS_BIN || join(process.env.HOME!,'intelFPGA_lite/21.1/quartus/bin');
 function run(tool:string,argv:string[]){
  console.log(tool);
  const fd=openSync(join(out,tool+'.log'),'w');
  try{
   const r=spawnSync(join(bin,tool),argv,{cwd:out,stdio:['ignore',fd,fd]});
   if(r.error || r.status!==0)throw Error(`${tool} failed: ${r.error || r.status}; see ${out}/${tool}.log`);
  }finally{closeSync(fd);}
 }
 run('quartus_map',['bench']);
 run('quartus_fit',['bench','--effort=standard']);
 run('quartus_sta',['bench']);
 const summary=readFileSync(join(out,'output_files','bench.sta.summary'),'utf8');
 const slack=[...summary.matchAll(/^Slack\s*:\s*([-+\d.]+)/gm)].map(m=>Number(m[1]));
 if(!slack.length || slack.some(s=>!Number.isFinite(s)||s<0))throw Error('Timing failed or summary missing; refusing to generate a deployable image');
 // External SRAM and ADC I/O still require bench qualification.
 run('quartus_asm',['bench']);
 run('quartus_cpf',['-c','-o','bitstream_compression=off','output_files/bench.sof','bench.rbf']);
 if(statSync(join(out,'bench.rbf')).size!==368011)throw Error('Unexpected FPGA image size');
 const sha256=createHash('sha256').update(readFileSync(join(out,'bench.rbf'))).digest('hex');
 writeFileSync(join(out,'image.json'),JSON.stringify({image:'general-sram-trigger',sha256,minimumInternalSlackNs:Math.min(...slack),inputs,benchQualified:false},null,2)+'\n');
 // One atomic pointer publishes the image and its matching provenance together.
 const next=join(products,'next-'+basename(out));
 symlinkSync(basename(out),next);
 renameSync(next,join(products,'current'));
 console.log(`Image: ${products}/current/bench.rbf; minimum internal slack ${Math.min(...slack)} ns`);
}
