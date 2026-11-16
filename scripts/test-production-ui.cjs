const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

function element() {
  return {children:[],dataset:{},listeners:{},classList:{add(){},remove(){},toggle(){}},value:'',hidden:true,
    textContent:'',disabled:false,scrollHeight:0,scrollTop:0,clientHeight:0,
    append(...items){this.children.push(...items);},replaceChildren(...items){this.children=items;},
    setAttribute(name,value){this[name]=value;},addEventListener(name,callback){this.listeners[name]=callback;},
    focus(){},closest(){return null;}};
}
async function appHarness({production=true,composer=true,capabilities,messages=[]}={}) {
  const nodes=new Map(),selectors=new Map(),requests=[];
  const get=id=>{if(!nodes.has(id))nodes.set(id,element());return nodes.get(id);};
  const document={hidden:false,body:{dataset:{production:String(production)}},activeElement:{tagName:'BODY'},
    getElementById:id=>!composer&&['message-form','message','send','character-count'].includes(id)?null:get(id),
    createElement:()=>element(),createDocumentFragment:()=>element(),querySelectorAll:()=>[],addEventListener(){},
    querySelector(selector){if(selector==='meta[name="csrf-token"]')return{content:'test-token'};
      if(!composer&&selector==='input[name=channel]:checked')return null;
      if(!selectors.has(selector))selectors.set(selector,element());return selectors.get(selector);}};
  const snapshot={cursor:0,messages,facts:[],episodes:[],pending:0,failed:0};
  const window={addEventListener(){},dispatchEvent(){}};
  vm.runInNewContext(fs.readFileSync(path.join(__dirname,'../web/app.js'),'utf8'),{document,window,Intl,Date,URLSearchParams,
    localStorage:{getItem(){return null;},setItem(){}},location:{assign(){}},CustomEvent:class{},EventSource:class{},
    requestAnimationFrame:callback=>callback(),setTimeout(){},clearTimeout(){},
    crypto:{randomUUID:()=> 'test-key'},FormData:class{get(){return 'dashboard';}},
    fetch:async(url,options)=>{requests.push({url,...options});return{ok:true,json:async()=>({csrf:'test-token',mode:production?'production':'development',processor:production?'disabled':'deterministic',capabilities,agents:[{id:'A01',name:'Atlas'},{id:'A05',name:'Cora'}],snapshot})};}});
  await new Promise(resolve=>setImmediate(resolve));
  return{nodes,requests,get};
}

test('production history loads with the conversation composer absent',async()=>{
  const h=await appHarness({composer:false});
  assert.ok(h.requests.some(request=>request.url.startsWith('/api/state')));
  assert.equal(h.get('queue-status').textContent,'Somente leitura');
});

test('production empty history does not create a preference prompt',async()=>{
  const h=await appHarness();
  const empty=h.get('facts').children[0].children[0];
  assert.equal(empty.children.some(node=>node.dataset.prompt),false);
  assert.equal(h.get('message').disabled,true);
});

test('a production composer submit cannot send a conversation request',async()=>{
  const h=await appHarness();h.get('message').value='Texto que não deve ser enviado';
  await h.get('message-form').listeners.submit({preventDefault(){},currentTarget:h.get('message-form')});
  assert.equal(h.requests.filter(request=>request.method==='POST').length,0);
  assert.equal(h.get('send').disabled,true);
});

test('server conversation capabilities disable sending even without a production body flag',async()=>{
  const h=await appHarness({production:false,capabilities:{conversation:'not_configured'}});
  assert.equal(h.get('message').disabled,true);
  assert.equal(h.get('queue-status').textContent,'Somente leitura');
});

test('development keeps the deterministic preference prompt available',async()=>{
  const h=await appHarness({production:false});
  const empty=h.get('facts').children[0].children[0];
  assert.equal(empty.children.some(node=>node.dataset.prompt==='Minha reserva é de 6 meses'),true);
  assert.equal(h.get('queue-status').textContent,'Tudo registrado');
});

test('configured production allows authenticated dashboard conversation submission',async()=>{
  const h=await appHarness({capabilities:{models:'configured',conversation:'configured'}});
  assert.equal(h.get('message').disabled,false);
  h.get('message').value='Cora, como organizar meu orçamento?';
  await h.get('message-form').listeners.submit({preventDefault(){},currentTarget:h.get('message-form')});
  const posted=h.requests.find(r=>r.url==='/api/conversations/turn'&&r.method==='POST');
  assert.ok(posted);
  assert.equal(posted.headers['X-CSRF-Token'],'test-token');
  assert.equal(h.requests.some(r=>r.url==='/demo/telegram'),false);
});

test('model reply stays text and names the profile that actually ran',async()=>{
  const unsafe='<img src=x onerror=alert(1)>';
  const h=await appHarness({capabilities:{models:'configured',conversation:'configured'},messages:[{id:2,direction:'outgoing',channel:'dashboard',status:'complete',created_at:'2026-11-05T12:00:00Z',text:unsafe,analysis:true,agent_id:'A05',source_event_id:1}]});
  const message=h.get('messages').children[0].children[0];
  const content=message.children[1];
  assert.equal(content.children[0].children[0].textContent,'Cora');
  assert.equal(content.children[1].textContent,unsafe);
  assert.equal(content.children[1].children.length,0);
});
