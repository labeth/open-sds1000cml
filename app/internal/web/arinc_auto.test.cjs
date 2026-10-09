// ENGMODEL-OWNER-UNIT: FU-APP-WEB
const fs=require('node:fs'),vm=require('node:vm'),assert=require('node:assert/strict');
const c={};vm.createContext(c);vm.runInContext(fs.readFileSync('decode.js','utf8')+'\n'+fs.readFileSync('decode_arinc429.js','utf8'),c);
const wave=[];
for(let i=0;i<9;i++){
 const word=[0x92345678,0x12345678,0x956789ab][i%3];
 for(let b=0;b<32;b++)wave.push(...Array(20).fill((word>>>b)&1?168:84),...Array(20).fill(133));
 wave.push(...Array(320).fill(133));
}
for(let phase=0;phase<120;phase++){
 const a=wave.slice(phase*40,phase*40+8000);
 const r=c.autodetect({c1:a,c2:null,dt_s:2.5e-7,cols:a.length},{fmt:'hex'});
 assert.equal(r.proto,'arinc429',`phase ${phase}: partial/error words must not become UART`);
}
console.log('ARINC automatic detection across partial and parity-error words passes');
