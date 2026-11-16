const test=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const vm=require('node:vm');
const path=require('node:path');

function el(tag='div'){return{tagName:tag,children:[],textContent:'',dataset:{},hidden:false,disabled:false,listeners:{},
 append(...nodes){this.children.push(...nodes);},replaceChildren(...nodes){this.children=nodes;},setAttribute(key,value){this[key]=value;},addEventListener(key,fn){this.listeners[key]=fn;},
 set innerHTML(value){throw Error('Untrusted output must use textContent');}};}
const ready=()=>({enabled:true,paused:false,state:'active',heartbeat_at:new Date().toISOString(),next_collect_at:'2026-11-08T00:15:00Z',next_run_at:'2026-11-08T06:00:00Z',daily_limit:30,auto_daily_limit:20,attempts:7,auto_attempts:5,sources:[{name:'Fonte pública',url:'https://agenciabrasil.ebc.com.br/rss/economia/feed.xml',state:'ok',last_success_at:'2026-11-07T20:00:00Z'}],runs:[{id:'00000000-0000-4000-8000-000000000099',status:'complete',topic:'fixed_income',reason:'scheduled',created_at:'2026-11-07T20:00:00Z'}],tasks:[]});
function harness(initial=ready()){
 const ids=['autonomy-panel','autonomy-state','autonomy-heartbeat','autonomy-next-run','autonomy-next-collect','autonomy-budget','autonomy-feedback','autonomy-pause','autonomy-run','autonomy-sources','autonomy-runs','autonomy-tasks','autonomy-detail','autonomy-strategies'];
 const nodes=new Map(ids.map(id=>[id,el()])),listeners={},requests=[],events=[];let state=initial;
 const document={hidden:false,getElementById:id=>nodes.get(id),createElement:el,querySelector:()=>({content:'csrf-fixture'}),addEventListener:(event,fn)=>listeners[event]=fn};
 vm.runInNewContext(fs.readFileSync(path.join(__dirname,'../web/autonomy.js'),'utf8'),{document,window:{addEventListener:(e,f)=>listeners[e]=f,dispatchEvent:e=>events.push(e)},fetch:async(url,options={})=>{requests.push({url,options});return{ok:true,status:200,json:async()=>url.endsWith('/strategies')?[]:state};},Intl,Date,JSON,AbortController,CustomEvent:class{constructor(type,options){this.type=type;this.detail=options?.detail;}},location:{assign(){}},setTimeout:()=>1,clearTimeout(){}});
 return{nodes,requests,listeners,events,setState(s){state=s;},async settle(){await new Promise(setImmediate);}};
}
function text(n){return n.textContent+n.children.map(text).join(' ');}
test('autonomy displays real heartbeat, schedule, budget and source state',async()=>{
 const h=harness();await h.settle();
 assert.match(h.nodes.get('autonomy-state').textContent,/ativo/i);
 assert.match(h.nodes.get('autonomy-budget').textContent,/5.*20.*7.*30/);
 assert.match(text(h.nodes.get('autonomy-runs')),/concluída/i);
 assert.match(text(h.nodes.get('autonomy-sources')),/Fonte pública/);
});
test('a stale heartbeat never presents the supervisor as active',async()=>{
 const h=harness({...ready(),heartbeat_at:'2000-01-01T00:00:00Z'});await h.settle();
 assert.match(h.nodes.get('autonomy-state').textContent,/sem atualização/i);
});
test('pause posts CSRF and explicit state, then reflects persisted paused state',async()=>{
 const h=harness();await h.settle();h.setState({...ready(),paused:true,state:'paused'});
 await h.nodes.get('autonomy-pause').listeners.click();await h.settle();
 const post=h.requests.find(r=>r.options.method==='POST');
 assert.equal(post.url,'/api/autonomy/pause');assert.equal(post.options.headers['X-CSRF-Token'],'csrf-fixture');assert.equal(JSON.parse(post.options.body).paused,true);
 assert.match(h.nodes.get('autonomy-state').textContent,/pausado/i);
});
test('source and model text stay literal and disabled autonomy cannot start a round',async()=>{
 const h=harness({...ready(),enabled:false,sources:[{name:'<script>unsafe()</script>',state:'error'}]});await h.settle();
 assert.match(text(h.nodes.get('autonomy-sources')),/<script>unsafe\(\)<\/script>/);
 assert.equal(h.nodes.get('autonomy-run').disabled,true);
});

test('task labels use the persisted responsible agent and unavailable source state',async()=>{
 const state=ready();state.tasks=[{run_id:state.runs[0].id,stage:1,agent_id:'A11',status:'complete'}];state.sources[0].state='failed';
 const h=harness(state);await h.settle();
 h.listeners['conselho-agents']({detail:[{id:'A11',name:'Bento'}]});
 assert.match(text(h.nodes.get('autonomy-tasks')),/Bento/);assert.doesNotMatch(text(h.nodes.get('autonomy-tasks')),/Teseu/);
 assert.match(text(h.nodes.get('autonomy-sources')),/indisponível/);
});

test('the office receives the same persisted task status through a read-only event',async()=>{
 const state=ready();state.tasks=[{run_id:state.runs[0].id,agent_id:'A17',stage:2,status:'running'}];
 const h=harness(state);await h.settle();const event=h.events.find(e=>e.type==='autonomy-status');
 assert.ok(event);assert.equal(event.detail,state);assert.equal(event.detail.tasks[0].agent_id,'A17');
 assert.ok(h.requests.every(r=>!r.options.method || r.options.method==='GET'));
});

test('a budget wait is explicit without hiding the scheduled run or reserved calls',async()=>{
 const h=harness({...ready(),state:'waiting_budget',reserved_calls:4});await h.settle();
 assert.match(h.nodes.get('autonomy-state').textContent,/aguardando cota/i);
 assert.match(h.nodes.get('autonomy-budget').textContent,/4 reservadas/);
 assert.doesNotMatch(h.nodes.get('autonomy-next-run').textContent,/não registrado|pausada/i);
});

test('activity events refresh the persisted status immediately with a read-only request',async()=>{
 const h=harness(); await h.settle();
 const count=h.requests.length;
 h.setState({...ready(),paused:true});
 h.listeners['office-event']({detail:{event_type:'autonomy.changed',payload:{}}});
 await h.settle();
 assert.deepEqual(h.requests.slice(count).map(r=>r.url),['/api/autonomy']);
 assert.equal(h.events.filter(e=>e.type==='autonomy-status').at(-1).detail.paused,true);
});
