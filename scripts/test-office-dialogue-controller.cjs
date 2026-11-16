const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const path = require('node:path');
const model = require('../web/office-dialogue-model.js');
const id = '00000000-0000-4000-8000-000000000099';
const now = Date.parse('2026-11-08T12:00:00Z');
function harness(pending = false) {
  class Target {
    constructor() { this.listeners = {}; this.children = []; this.style = {setProperty(){}}; this.dataset = {}; this.offsetWidth = 200; this.offsetHeight = 100; }
    addEventListener(type, fn) { (this.listeners[type] ||= []).push(fn); }
    dispatchEvent(event) { for (const fn of this.listeners[event.type] || []) fn(event); }
    append(...children) { for (const child of children) { child.remove(); child.parentNode=this; this.children.push(child); } }
    remove() { if (this.parentNode) this.parentNode.children=this.parentNode.children.filter(child=>child!==this); this.parentNode=null; }
    contains(child) { return child===this || this.children.some(el=>el.contains(child)); }
    querySelector(tag) { return this.children.find(el=>el.tag===tag); }
    replaceChildren() { if (this.children.includes(document.activeElement)) document.activeElement = null; this.children = []; }
    setAttribute() {}
    focus() { document.activeElement = this; }
  }
  const elements = Object.fromEntries(['office-dialogue-layer','office-dialogue','office-dialogue-label','dialogue-prev','dialogue-next','dialogue-pause','dialogue-focus','dialogue-report'].map(key=>[key,new Target()]));
  const document = new Target(); document.getElementById = key=>elements[key]; document.createElement = tag=>Object.assign(new Target(),{tag});
  elements['office-dialogue-layer'].clientWidth=1000; elements['office-dialogue-layer'].clientHeight=700;
  const window = new Target(); window.ConselhoDialogueModel=model; window.ConselhoOffice={anchorFor:()=>({x:500,y:350})};
  const tasks = [0,1].map(stage=>({id:'complete-'+stage,run_id:id,stage,agent_id:stage?'A11':'A13',status:'complete',completed_at:new Date(now).toISOString(),result:stage?{reply:'Estratégia registrada.'}:{summary:'Pesquisa registrada.'}}));
  tasks.push({id:'running',run_id:id,stage:2,agent_id:'A17',status:pending?'pending':'running',lease_expires_at:new Date(now+120000).toISOString()});
  const run={id,status:'running',created_at:new Date(now).toISOString()};
  const reads=[];
  vm.runInNewContext(fs.readFileSync(path.join(__dirname,'../web/office-dialogue.js'),'utf8'),{window,document,matchMedia:()=>({matches:false,addEventListener(){}}),location:{search:'',pathname:'/dashboard'},URLSearchParams,Intl,Date:class extends Date { static now(){return now;} },AbortController,CustomEvent:class { constructor(type,options){this.type=type;this.detail=options.detail;} },setTimeout:()=>1,clearTimeout(){},fetch:async(url,options)=>{reads.push({url,options});return {ok:true,status:200,json:async()=>({run,tasks})};}});
  function dispatch(type,detail) { window.dispatchEvent({type,detail}); }
  return {window,document,elements,reads,dispatch,async status(enabled=true,age=0,extra={}){
    dispatch('autonomy-status',{enabled,heartbeat_at:new Date(now-age).toISOString(),runs:[run],tasks,...extra});
    await new Promise(resolve=>setImmediate(resolve));
  },text(){
    function copy(el) { return [el.textContent,...el.children.map(copy)].filter(Boolean).join(' '); }
    return copy(elements['office-dialogue-layer']);
  }};
}

test('live work has priority over a newer queued round and stale work drops its balloon',async()=>{
  const h=harness();
  await h.status();
  assert.match(h.text(),/Avaliando os riscos/);
  assert.match(h.text(),/Proposta em revisão/);
  await h.status(true,0,{runs:[{id:'00000000-0000-4000-8000-000000000100',status:'pending'},{id,status:'running'}]});
  assert.equal(h.window.ConselhoDialogue.getState().active,'A17');
  assert.equal(h.window.ConselhoDialogue.getState().runId,id);
  await h.status(true,180001);
  assert.doesNotMatch(h.text(),/Avaliando os riscos|Proposta em revisão/);
  assert.equal(h.window.ConselhoDialogue.getState().active,null);
});

test('background refresh keeps the focused balloon and historical reading stays explicit',async()=>{
  const h=harness();
  await h.status();
  const layer=h.elements['office-dialogue-layer'];
  const bubble=layer.children.find(el=>el.dataset.agent==='A17');
  const focus=bubble.querySelector('button');
  focus.focus();
  await h.status(true,1000);
  assert.equal(h.document.activeElement,focus);
  assert.equal(h.reads.length,1);
  assert.doesNotMatch(h.text(),/Pesquisa registrada/);
  h.elements['dialogue-next'].dispatchEvent({type:'click'});
  assert.match(h.text(),/Histórico · Pesquisa registrada/);
  h.elements['dialogue-pause'].dispatchEvent({type:'click'});
  assert.doesNotMatch(h.text(),/Pesquisa registrada/);
});

test('selecting a queued character explains the wait without claiming execution',async()=>{
  const h=harness(true);
  await h.status(true,0,{state:'waiting_budget'});
  h.dispatch('conselho-state',{active_jobs:[{agent_id:'A23',status:'pending',error_code:'waiting_budget'}]});
  h.dispatch('office-agent-selected',{id:'A23'});
  assert.match(h.text(),/Aguardando cota/);
  assert.equal(h.window.ConselhoDialogue.getState().active,null);
});
