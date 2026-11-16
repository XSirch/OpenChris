const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const path = require('node:path');
function harness(fetch) {
  const fields = [['action','expense-create'],['idempotency_key','stable-key-123'],['description','Mercado'],['amount','100,50']];
  const feedback = {textContent:''}, button = {textContent:'Salvar gasto',disabled:false};
  let submit, redirect;
  const form = {action:{tagName:'INPUT',value:'expense-create'},getAttribute:name=>name==='action'?'/expenses/actions':null,reportValidity:()=>true,
    querySelector:selector=>selector==='.finance-form-feedback'?feedback:button,addEventListener(type,callback){if(type==='submit')submit=callback;}};
  const document = {querySelectorAll:selector=>selector==='[data-finance-form]'?[form]:[]};
  vm.runInNewContext(fs.readFileSync(path.join(__dirname,'../web/finance-forms.js'),'utf8'),{document,fetch,URLSearchParams,
    FormData:class { [Symbol.iterator](){return fields[Symbol.iterator]();} },TypeError,
    location:{assign(value){redirect=value;}}});
  return {submit:()=>submit({preventDefault(){}}),feedback,button,fields,redirect:()=>redirect};
}
test('a control named action cannot shadow the financial form endpoint',async()=>{
  let destination, body;
  const h=harness(async(url,options)=>{destination=url;body=options.body;return {ok:true,json:async()=>({redirect:'/expenses?saved=expense'})};});
  await h.submit();
  assert.equal(destination,'/expenses/actions');
  assert.equal(body.get('action'),'expense-create');
  assert.equal(h.redirect(),'/expenses?saved=expense');
});
test('network retry keeps the same key and restores editable fields',async()=>{
  const keys=[];let attempts=0;
  const h=harness(async(url,options)=>{keys.push(options.body.get('idempotency_key'));if(!attempts++)throw new TypeError('network');return {ok:true,json:async()=>({redirect:'/expenses'})};});
  await h.submit();
  assert.equal(h.button.disabled,false);
  assert.equal(h.button.textContent,'Salvar gasto');
  assert.match(h.feedback.textContent,/campos foram mantidos/);
  await h.submit();
  assert.deepEqual(keys,['stable-key-123','stable-key-123']);
});
test('unexpected server content shows a recovery message without parser details',async()=>{
  const h=harness(async()=>({ok:false,json:async()=>{throw new SyntaxError('Unexpected non-whitespace character after JSON');}}));
  await h.submit();
  assert.match(h.feedback.textContent,/campos foram mantidos/);
  assert.doesNotMatch(h.feedback.textContent,/Unexpected|JSON/);
});
