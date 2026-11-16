// Pack generated seated bodies without redrawing or transforming their anatomy.
// Usage: node docs/design/pack-seated-atlas.cjs <generated-a.png> <generated-b.png> <generated-c.png>
const fs = require('node:fs');
const path = require('node:path');
const {execFileSync} = require('node:child_process');
const root = path.resolve(__dirname,'../..');
const sources = process.argv.slice(2);
if (sources.length !== 3) throw new Error('Supply the three generated seated reference sheets.');
const metadata = JSON.parse(execFileSync('python',['-c',[
  'import importlib.util,json,sys',
  'from PIL import Image',
  "s=importlib.util.spec_from_file_location('office_inspect','scripts/inspect_office_assets.py')",
  "m=importlib.util.module_from_spec(s);sys.modules['office_inspect']=m;s.loader.exec_module(m)",
  'result=[]',
  'for source in sys.argv[1:]:',
  ' im=Image.open(source); parts=m.connected_components(im.getchannel("A").tobytes(),*im.size)',
  ' bodies=[p for p in parts if p.area>2000]',
  ' assert len(bodies)==24',
  ' groups={p.nearest_cell():p for p in bodies}',
  ' frames=[]',
  ' for row in range(4):',
  '  for col in [0,3]:',
  '   p=groups[(row,col)];frames.append(dict(x=p.left-2,y=p.top-2,w=p.right-p.left+4,h=p.bottom-p.top+4))',
  ' result.append(frames)',
  'print(json.dumps(result))'
].join('\n'),...sources],{cwd:root,encoding:'utf8'}));
const filters=[],layout=[],frames=[];
for(let sheet=0;sheet<3;sheet++) {
  const count=sheet===2?7:8;
  filters.push(`[${sheet}:v]format=rgba,split=${count}${Array.from({length:count},(_,n)=>`[s${sheet}_${n}]`).join('')}`);
  for(let n=0;n<count;n++) {
    const frame=metadata[sheet][n],index=frames.length,x=Math.floor((256-frame.w)/2),y=256-frame.h;
    if(frame.w>256||frame.h>256) throw new Error('A generated sprite exceeds its atlas cell.');
    filters.push(`[s${sheet}_${n}]crop=${frame.w}:${frame.h}:${frame.x}:${frame.y},pad=256:256:${x}:${y}:color=0x00000000[p${index}]`);
    layout.push(`${index%6*256}_${Math.floor(index/6)*256}`);
    frames.push({x:index%6*256+x,y:Math.floor(index/6)*256+y,w:frame.w,h:frame.h});
  }
}
filters.push(`${frames.map((_,n)=>`[p${n}]`).join('')}xstack=inputs=23:layout=${layout.join('|')}:fill=0x00000000[out]`);
const png=path.join(root,'assets/office/agents-seated.png'),webp=path.join(root,'assets/office/agents-seated.webp');
execFileSync('ffmpeg',['-y',...sources.flatMap(source=>['-i',source]),'-filter_complex',filters.join(';'),'-map','[out]','-frames:v','1',png],{stdio:'pipe'});
execFileSync('ffmpeg',['-y','-i',png,'-c:v','libwebp','-lossless','1','-compression_level','6',webp],{stdio:'pipe'});
const manifestPath=path.join(root,'assets/office/manifest.json'),manifest=JSON.parse(fs.readFileSync(manifestPath,'utf8'));
manifest.atlases=manifest.atlases.filter(atlas=>atlas.id!=='seated');
manifest.atlases.push({id:'seated',url:'/static/office/agents-seated.webp',width:1536,height:1024});
manifest.agents.forEach((agent,index)=>{agent.seated={atlas:'seated',frame:frames[index],height:100};});
manifest.world.seats=[
  {id:'planning-left',label:'Estação de planejamento 1',position:{x:650,y:245},approach:{x:636,y:300},facing:1,occluders:[[[604,149],[630,145],[641,185],[635,209],[607,203],[596,168]]]},
  {id:'planning-right',label:'Estação de planejamento 2',position:{x:812,y:313},approach:{x:780,y:372},facing:1,occluders:[[[768,212],[789,207],[805,253],[799,272],[771,263],[760,226]]]},
  {id:'research-desk',label:'Estação de pesquisa',position:{x:1343,y:399},approach:{x:1344,y:432},facing:1,occluders:[[[1305,283],[1326,279],[1343,325],[1335,352],[1308,342],[1297,300]]]},
  {id:'legal-desk',label:'Estação do Jurídico',position:{x:1384,y:769},approach:{x:1368,y:804},facing:1,occluders:[[[1330,645],[1359,647],[1380,679],[1372,707],[1341,694],[1323,665]]]}
];
fs.writeFileSync(manifestPath,JSON.stringify(manifest,null,2)+'\n','utf8');
console.log('Packed 23 seated identities; four existing workstation chairs mapped.');
