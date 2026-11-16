const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const path = require('node:path');

function harness(returnValue, destination, hash = '') {
  const makeNode = () => ({value:'',textContent:'',hidden:false,listeners:{},addEventListener(type,fn){this.listeners[type]=fn;},setAttribute(){},removeAttribute(){},focus(){},select(){}});
  const nodes = new Map(['#password','#toggle-password','#caps-lock','#login-error','#login-error-text','#login-refresh','#login-status'].map(id=>[id,makeNode()]));
  const returnTo={value:returnValue}, button=makeNode(), label=makeNode(), form=makeNode();
  button.querySelector=()=>label;
  form.action='https://conselho.example/login';
  form.querySelector=selector=>selector==='[name="return_to"]'?returnTo:button;
  nodes.set('#login-form',form);
  let redirected;
  const fetch=async()=>({url:destination,redirected:true,ok:true,status:200,json:async()=>({})});
  class FormData { [Symbol.iterator](){return [['return_to',returnTo.value]][Symbol.iterator]();} }
  const location={href:'https://conselho.example/login',origin:'https://conselho.example',hash,assign(value){redirected=value;}};
  vm.runInNewContext(fs.readFileSync(path.join(__dirname,'../web/login.js'),'utf8'),{document:{querySelector:selector=>nodes.get(selector)||null},window:{location,fetch,URLSearchParams,FormData},fetch,URL,URLSearchParams,FormData});
  return {returnTo,nodes,redirect:()=>redirected,submit:()=>form.listeners.submit({preventDefault(){}})};
}

test('login returns to the financial profile and preserves each supported page anchor',async()=>{
  for(const target of ['/finance/profile#risk_profile','/finance/profile#reserve-current-cents','/finance/profile#confirmed','/reports#report-reading','/reports#report-strategies','/reports#report-sources','/reports#task-4','/dashboard#connections-panel']) {
    const h=harness(target,'https://conselho.example'+target.split('#')[0]);
    await h.submit();
    assert.equal(h.redirect(),'https://conselho.example'+target);
  }
});

test('a browser fragment carried through login is retained only for its matching page',async()=>{
  const h=harness('/finance/profile','https://conselho.example/finance/profile','#goals');
  assert.equal(h.returnTo.value,'/finance/profile#goals');
  await h.submit();
  assert.equal(h.redirect(),'https://conselho.example/finance/profile#goals');
  const other=harness('/finance/profile','https://conselho.example/finance/profile','#task-2');
  assert.equal(other.returnTo.value,'/finance/profile');
});

test('login never follows an external response or restores an unsupported anchor',async()=>{
  const external=harness('/finance/profile#goals','https://outside.example/finance/profile');
  await external.submit();
  assert.equal(external.redirect(),undefined);
  assert.equal(external.nodes.get('#login-error').hidden,false);
  const invalid=harness('/finance/profile#unrecognized','https://conselho.example/finance/profile');
  await invalid.submit();
  assert.equal(invalid.redirect(),'https://conselho.example/finance/profile');
});
