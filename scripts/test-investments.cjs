const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const path = require('node:path');

function element(tag='div') {
  return {tagName:tag,children:[],dataset:{},textContent:'',hidden:false,open:false,
    append(...children){this.children.push(...children);},replaceChildren(...children){this.children=children;},
    set innerHTML(_value){throw Error('Provider values must use textContent');}};
}
function harness() {
  const nodes=new Map(['investment-panel','investment-state','investment-totals','investment-coverage','investment-stamp','investment-positions','investment-limit','investment-composition','investment-update-state'].map(id=>[id,element()]));
  const listeners={};
  vm.runInNewContext(fs.readFileSync(path.join(__dirname,'../web/investments.js'),'utf8'),{
    document:{getElementById:id=>nodes.get(id),createElement:element},
    window:{addEventListener:(name,fn)=>{listeners[name]=fn;}},Intl,Date,JSON});
  return {nodes,send(data){listeners['finance-summary']({detail:{investments:data}});},connection(state){listeners['finance-connection']({detail:{state}});}};
}
const portfolio=()=>({state:'available',active_count:1,withdrawn_count:2,unknown_count:0,missing_value_count:0,withheld_count:0,omitted:0,
  totals:[{currency:'BRL',balance:'9007199254740993.01',balance_text:'R$ 9.007.199.254.740.993,01',count:1,valued_count:1}],
  positions:[{name:'<script>unsafe()</script>',type:'FIXED_INCOME',type_text:'Renda fixa',status:'ACTIVE',status_text:'Ativo',balance_text:'R$ 9.007.199.254.740.993,01',issuer:'Emissor de teste',maturity:'2027-01-02',observed_at:'2026-11-07T11:00:00Z'}],last_synced_at:'2026-11-07T11:00:00Z',oldest_synced_at:'2026-11-07T10:00:00Z'});
function text(node){return node.textContent+node.children.map(text).join(' ');}

test('investment updates preserve exact formatted values, safe text and expanded details',()=>{
  const h=harness();const details=h.nodes.get('investment-composition');details.open=true;
  h.send(portfolio());
  assert.match(text(h.nodes.get('investment-totals')),/9\.007\.199\.254\.740\.993,01/);
  assert.match(text(h.nodes.get('investment-positions')),/<script>unsafe\(\)<\/script>/);
  assert.match(h.nodes.get('investment-coverage').textContent,/1 ativa.*2 resgatad/);
  assert.equal(details.open,true);
  assert.equal(h.nodes.get('investment-panel').dataset.state,'available');
});
test('unavailable snapshots clear prior totals and retain an actionable state',()=>{
  const h=harness();h.send(portfolio());h.send({state:'unavailable',totals:[],positions:[],active_count:0,withdrawn_count:0});
  assert.doesNotMatch(text(h.nodes.get('investment-totals')),/9\.007/);
  assert.match(h.nodes.get('investment-state').textContent,/indisponíveis/);
  assert.match(text(h.nodes.get('investment-totals')),/Conexões/);
});
test('stream interruptions qualify the preserved snapshot without claiming a new collection',()=>{
  const h=harness();h.send(portfolio());const stamp=h.nodes.get('investment-stamp').textContent;
  h.connection('stale');
  assert.match(h.nodes.get('investment-update-state').textContent,/interrompida/);
  assert.equal(h.nodes.get('investment-stamp').textContent,stamp);
  assert.match(text(h.nodes.get('investment-totals')),/9\.007/);
});
test('partial coverage and omitted positions remain visible',()=>{
  const h=harness();h.send({...portfolio(),state:'partial',unknown_count:2,missing_value_count:1,omitted:12});
  assert.match(h.nodes.get('investment-state').textContent,/parcial/);
  assert.match(h.nodes.get('investment-coverage').textContent,/1 sem valor/);
  assert.match(h.nodes.get('investment-limit').textContent,/12/);
});
