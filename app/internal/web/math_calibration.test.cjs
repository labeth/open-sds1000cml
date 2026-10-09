// ENGMODEL-OWNER-UNIT: FU-APP-WEB
// TRLC-LINKS: REQ-SDS-203
const assert=require('node:assert/strict'),fs=require('node:fs'),vm=require('node:vm');
const text=fs.readFileSync('app_draw.js','utf8');
const code=text.slice(text.indexOf('function computeMath()'),text.indexOf('function drawRefTrace'));
const c={frame:{c1:[103,153],c2:[103,128],vpc1:.04,vpc2:.08,off1_v:-1,off2_v:-2},st:{},mathMemo:{},fftCh:{},mathFn:''};
vm.createContext(c);vm.runInContext(code,c);
for(const [mode,want] of [['c1+c2',[128,228]],['c1-c2',[128,128]],['c2-c1',[128,128]],['c1*c2',[128,140.5]]]){
 c.mathFn=mode;assert.deepEqual(Array.from(c.computeMath()),want,mode);
}
c.mathFn='c1-c2';c.frame={c1:[153],c2:[153],vpc1:.08,vpc2:.08};c.st={zoom1:2};
assert.deepEqual(Array.from(c.computeMath()),[128],'equal tip volts despite zoom/probe');
c.frame={...c.frame,off1_v:1};assert.deepEqual(Array.from(c.computeMath()),[103],'scale/offset change invalidates cached result');
c.st.zoom1=1;assert.deepEqual(Array.from(c.computeMath()),[115.5],'stopped zoom change invalidates cached result');
c.frame={c1:[128,140],c2:[128],vpc1:.04,vpc2:.04};
assert.deepEqual(Array.from(c.computeMath()),[128,-1],'missing samples remain gaps');
console.log('ALL PASS');
