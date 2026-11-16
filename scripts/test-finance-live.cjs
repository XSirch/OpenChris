const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const path = require('node:path');
function harness(month='',responseStatus=200) {
  const nodes=new Map();
  for(const id of ['finance-live-state','live-net-worth','live-expenses','live-pending','live-top-category','live-top-amount','live-activity','live-wealth-source','finance-live-stamp','net-worth-editor','net-worth-assets','net-worth-liabilities','live-finance-month'])nodes.set(id,{textContent:'',dataset:{},open:false,value:''});
  nodes.get('finance-live-state').dataset.month=month;
  const listeners={},streams=[],requests=[];let redirected;
  const document={hidden:false,getElementById:id=>nodes.get(id),addEventListener(type,callback){listeners[type]=callback;}};
  class EventSource {
    constructor(url){this.url=url;this.listeners={};streams.push(this);}
    addEventListener(type,callback){this.listeners[type]=callback;}
    close(){this.closed=true;}
  }
  const window={addEventListener(){},dispatchEvent(){}};
  vm.runInNewContext(fs.readFileSync(path.join(__dirname,'../web/finance-live.js'),'utf8'),{document,window,EventSource,Intl,Date,
    CustomEvent:class{},location:{assign(value){redirected=value;}},fetch:async url=>{requests.push(url);return{status:responseStatus,ok:responseStatus===200};}});
  return {nodes,streams,requests,redirect:()=>redirected,async visibility(hidden){document.hidden=hidden;listeners.visibilitychange();}};
}
test('explicit dashboard month stays on the same SSE and authentication context',async()=>{
  const h=harness('2026-09');
  assert.equal(h.streams[0].url,'/api/finance/events?month=2026-09');
  await h.streams[0].onerror();
  assert.equal(h.requests[0],'/api/finance/summary?month=2026-09');
  assert.equal(h.nodes.get('finance-live-state').dataset.state,'stale');
});
test('an unauthorized reconnect redirects instead of staying live or retrying forever',async()=>{
  const h=harness('',401);
  await h.visibility(true);
  assert.equal(h.streams[0].closed,true);
  await h.visibility(false);
  assert.equal(h.streams.length,2);
  await h.streams[1].onerror();
  assert.equal(h.redirect(),'/login');
  assert.equal(h.streams[1].closed,true);
});
test('a live update preserves edits already open in the wealth form',()=>{
  const h=harness();h.nodes.get('net-worth-editor').open=true;h.nodes.get('net-worth-assets').value='123,45';
  h.streams[0].listeners['finance.snapshot']({data:JSON.stringify({mode:'manual',month_label:'outubro de 2026',expense_text:'R$ 100,50',net_worth_text:'R$ 2.000,00',pending_text:'Nenhuma pendência',top_category:'Moradia',top_category_text:'R$ 100,50',activity_text:'Nenhuma tarefa pendente',net_worth_date:'2026-10-27',snapshot_at:'2026-10-27T15:00:00Z',assets_text:'2500,00',liabilities_text:'500,00'})});
  assert.equal(h.nodes.get('net-worth-assets').value,'123,45');
  assert.equal(h.nodes.get('live-net-worth').textContent,'R$ 2.000,00');
  assert.equal(h.nodes.get('finance-live-state').dataset.state,'live');
});

test('bank summaries update expenses while keeping manual wealth clearly identified',()=>{
  const h=harness();
  h.streams[0].listeners['finance.snapshot']({data:JSON.stringify({mode:'bank',month_label:'novembro de 2026',expense_text:'R$ 250,00',net_worth_text:'R$ 5.000,00',net_worth_date:'2026-11-01',snapshot_at:'2026-11-13T15:00:00Z'})});
  assert.equal(h.nodes.get('live-expenses').textContent,'R$ 250,00');
  assert.match(h.nodes.get('live-wealth-source').textContent,/informado por você/);
  assert.match(h.nodes.get('finance-live-stamp').textContent,/gastos bancários via Pluggy/);
  assert.equal(h.nodes.get('finance-live-state').dataset.state,'live');
});
