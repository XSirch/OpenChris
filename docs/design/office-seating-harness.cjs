const fs=require('node:fs'),path=require('node:path'),vm=require('node:vm');
const root=path.resolve(__dirname,'../..');
function target(){const listeners=new Map();return {addEventListener(type,fn){if(!listeners.has(type))listeners.set(type,[]);listeners.get(type).push(fn);},dispatchEvent(event){for(const fn of listeners.get(event.type)||[])fn(event);}};}
function element(id='',tag='div',draws=[]){
  const attrs=new Map(),classes=new Set();
  return Object.assign(target(),{id,tagName:tag,dataset:{},children:[],hidden:false,disabled:false,textContent:'',value:'',isConnected:true,
    classList:{toggle(n,v){if(v)classes.add(n);else classes.delete(n);},remove(...names){names.forEach(n=>classes.delete(n));}},
    append(...children){this.children.push(...children);},replaceChildren(...children){this.children=children.flatMap(c=>c.tagName==='fragment'?c.children:[c]);},
    setAttribute(n,v){attrs.set(n,v);},getAttribute(n){return attrs.get(n);},focus(){},getBoundingClientRect(){return {left:0,top:0,width:1000,height:750};},getClientRects(){return [this.getBoundingClientRect()];},
    querySelector(selector){return this.children.find(c=>selector==='canvas'?c.tagName==='canvas':c.className?.split(' ').includes(selector.slice(1)))||this.children.map(c=>c.querySelector?.(selector)).find(Boolean)||null;},
    getContext(){return new Proxy({imageSmoothingEnabled:false,measureText(text){return {width:String(text).length*5};},fillText(text){draws.push({type:'fillText',text});}},{get(o,k){return k in o?o[k]:()=>{};}});},setPointerCapture(){},hasPointerCapture(){return false;},releasePointerCapture(){},closest(){return null;}});
}
module.exports=async function harness(options={}){
  const manifest=JSON.parse(fs.readFileSync(path.join(root,'assets/office/manifest.json'),'utf8'));
  const draws=[];
  const elements=new Map(['office-canvas','office-viewport','agent-roster','agent-panel','agent-name','agent-state','agent-current-task','office-selection','walk-agent','send-agent-home','tour-office','pause-office','office-status'].map(id=>[id,element(id,'div',draws)]));
  if(options.viewport) for(const id of ['office-canvas','office-viewport']) elements.get(id).getBoundingClientRect=()=>({left:0,top:0,...options.viewport});
  const document=Object.assign(target(),{hidden:false,body:{dataset:options.production ? {production:"true",codexEnabled:"true"} : {}},getElementById:id=>elements.get(id)||null,createElement:tag=>element('',tag),createDocumentFragment:()=>element('','fragment'),querySelectorAll:()=>[]});
  const window=Object.assign(target(),{devicePixelRatio:1,ConselhoOfficeModel:require(path.join(root,'web/office-model.js'))});
  const displayPath=path.join(root,'web/office-display.js');
  if(fs.existsSync(displayPath))window.ConselhoOfficeDisplay=require(displayPath);
  const media=Object.assign(target(),{matches:Boolean(options.reduced)});let requests=0,next=0,frames=new Map(), timers=new Map();
  const context={window,document,console,setTimeout(fn){timers.set(++next,fn);return next;},clearTimeout(id){timers.delete(id);},matchMedia:()=>media,localStorage:{getItem(){return options.paused?'true':null;},setItem(){}},requestAnimationFrame(fn){frames.set(++next,fn);return next;},cancelAnimationFrame(id){frames.delete(id);},CustomEvent:class{constructor(type,init={}){this.type=type;this.detail=init.detail;}},fetch:async()=>{requests++;return {ok:true,json:async()=>manifest};},Image:class{set src(url){const asset=manifest.atlases.find(a=>a.url===url)||manifest.world;this.naturalWidth=asset.width;this.naturalHeight=asset.height;queueMicrotask(()=>this.onload());}}};
  vm.runInNewContext(fs.readFileSync(path.join(root,'web/office.js'),'utf8'),context);await new Promise(resolve=>setImmediate(resolve));
  return {office:window.ConselhoOffice,window,document,media,elements,manifest,draws,requests:()=>requests,fireTimers(){const scheduled=[...timers.values()];timers.clear();scheduled.forEach(fn=>fn());},tick(time){const callbacks=[...frames.values()];frames.clear();callbacks.forEach(fn=>fn(time));}};
};
