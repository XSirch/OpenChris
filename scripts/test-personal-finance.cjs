const test = require('node:test');
const assert = require('node:assert/strict');
const {parseCents, profileInput, safeActionURL} = require('../web/personal-finance.js');
const fs = require('node:fs');
const vm = require('node:vm');
const path = require('node:path');

test('financial form preserves unknown values and explicit zero without rounding', () => {
  assert.equal(parseCents(''), null);
  assert.equal(parseCents('0'), 0);
  assert.equal(parseCents('1.234,56'), 123456);
  assert.equal(parseCents('12.34'), 1234);
  assert.throws(() => parseCents('12,345'));
  assert.throws(() => parseCents('-1'));
  assert.throws(() => parseCents('9007199254740993'));
});

test('profile request only carries confirmed user fields and preserves missing amounts', () => {
  const profile = profileInput({version:3, confirmed:false, goals:[{name:'Reserva',target:'10000,50',target_date:'2027-12-01'}], reserve_current_cents:'0',reserve_target_cents:'10000',commitments_30_days_cents:'',minimum_checking_cents:'500',protected_checking_cents:'0',risk_profile:'conservative',experience:'beginner',liquidity_days:'0',restrictions:'Sem cripto',updated_at:'untrusted'});
  assert.equal(profile.version, 3);
  assert.equal(profile.reserve_current_cents, 0);
  assert.equal(profile.commitments_30_days_cents, null);
  assert.equal(profile.liquidity_days, 0);
  assert.equal(profile.goals[0].target_cents, 1000050);
  assert.equal(profile.confirmed, false);
  assert.equal(Object.hasOwn(profile, 'updated_at'), false);
});

test('pending actions can only lead to financial pages on this application', () => {
  assert.equal(safeActionURL('/finance/profile#risk_profile'), '/finance/profile#risk_profile');
  assert.equal(safeActionURL('/expenses?source=bank'), '/expenses?source=bank');
  assert.equal(safeActionURL('/dashboard#connections-panel'), '/dashboard#connections-panel');
  assert.equal(safeActionURL('javascript:alert(1)'), '/finance/profile');
  assert.equal(safeActionURL('//example.com'), '/finance/profile');
  assert.equal(safeActionURL('/logout'), '/finance/profile');
});

test('personal data refreshes only after a changed finance revision and retries failed reads', async () => {
  const makeNode = () => ({textContent:'',children:[],append(...nodes){this.children.push(...nodes);},replaceChildren(...nodes){this.children=nodes;}});
  const status=makeNode(), issues=makeNode(), listeners={}, calls=[];
  let revision='first', unavailable=false;
  const panel={querySelector:selector=>selector==='[data-personal-status]'?status:selector==='[data-personal-issues]'?issues:null};
  const document={querySelectorAll:selector=>selector==='[data-personal-finance]'?[panel]:[],getElementById:()=>null,querySelector:()=>({content:'csrf'}),createElement:makeNode};
  vm.runInNewContext(fs.readFileSync(path.join(__dirname,'../web/personal-finance.js'),'utf8'),{document,window:{addEventListener:(name,fn)=>listeners[name]=fn},Intl,Date,AbortController,setTimeout,clearTimeout,fetch:async url=>{calls.push(url);return{ok:!unavailable,status:unavailable?503:200,json:async()=>({revision,pending_items:[],can_recommend:false})};}});
  await new Promise(setImmediate);
  assert.equal(calls.length,1);
  await listeners['finance-summary']({detail:{personal_revision:'first'}});
  await listeners['finance-summary']({detail:{}});
  assert.equal(calls.length,1);
  revision='second';
  await listeners['finance-summary']({detail:{personal_revision:revision}});
  await listeners['finance-summary']({detail:{personal_revision:revision}});
  assert.equal(calls.length,2);
  unavailable=true; revision='third';
  await listeners['finance-summary']({detail:{personal_revision:revision}});
  assert.match(status.textContent,/Tente novamente/);
  unavailable=false;
  await listeners['finance-summary']({detail:{personal_revision:revision}});
  assert.equal(calls.length,4);
});
