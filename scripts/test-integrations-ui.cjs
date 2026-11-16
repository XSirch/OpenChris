const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

class Element {
  constructor(tag = 'div') { this.tagName = tag.toUpperCase(); this.children = []; this.dataset = {}; this.listeners = {}; this.hidden = false; this.disabled = false; this.textContent = ''; this.attributes = {}; this.value = ''; }
  append(...children) { this.children.push(...children); }
  replaceChildren(...children) { this.children = children; }
  setAttribute(name, value) { this.attributes[name] = String(value); }
  removeAttribute(name) { delete this.attributes[name]; }
  addEventListener(name, callback) { this.listeners[name] = callback; }
  remove() { this.removed = true; }
  focus() { this.focused = true; }
  closest(selector) { return selector === '[data-sync-item]' && this.dataset.syncItem ? this : null; }
  querySelectorAll(selector) { return selector === 'button, input' ? descendants(this).filter(node => ['BUTTON', 'INPUT'].includes(node.tagName)) : []; }
}
const descendants = element => element.children.flatMap(child => [child, ...descendants(child)]);
const copy = element => [element.textContent, ...element.children.map(copy)].join(' ');
const tick = () => new Promise(resolve => setImmediate(resolve));
const response = (body, status = 200) => ({ok:status >= 200 && status < 300, status, json:async () => body});

async function harness({handler = () => response({}), initialPanel = '', hasRoot = true} = {}) {
  const nodes = new Map();
  const ids = ['integrations-root', 'integrations-status', 'integrations-refresh', 'telegram-status', 'telegram-detail', 'telegram-health', 'telegram-setup', 'pluggy-status', 'pluggy-detail', 'pluggy-health', 'pluggy-setup', 'pluggy-connect', 'pluggy-data-open', 'pluggy-items', 'pluggy-register-form', 'pluggy-item-id', 'pluggy-register', 'pluggy-data', 'pluggy-data-status', 'pluggy-data-content', 'bank-source-label'];
  ids.forEach(id => { const node = new Element(id.includes('setup') || ['integrations-refresh', 'pluggy-connect', 'pluggy-data-open', 'pluggy-register'].includes(id) ? 'button' : id === 'pluggy-item-id' ? 'input' : 'div'); node.id = id; nodes.set(id, node); });
  const root = nodes.get('integrations-root'); root.append(...[...nodes.values()].filter(node => node !== root));
  const requests = [], events = {}, timers = new Map(); let timerId = 0;
  const document = {body:{dataset:{production:'true',drawerOpen:initialPanel}}, head:new Element('head'), hidden:false,
    getElementById:id => id === 'integrations-root' && !hasRoot ? null : nodes.get(id) || null,
    createElement:tag => new Element(tag), querySelector:selector => selector === 'meta[name="csrf-token"]' ? {content:'csrf-private'} : null};
  const window = {addEventListener:(name, callback) => { events[name] = callback; }};
  const logs = [];
  const context = {document, window, Intl, Date, AbortController, URLSearchParams, location:{assign:value => { window.redirect = value; }},
    console:{log:(...args) => logs.push(args), error:(...args) => logs.push(args)},
    setTimeout:(callback, delay) => { timers.set(++timerId, {callback, delay}); return timerId; }, clearTimeout:id => timers.delete(id),
    fetch:async (url, options) => { requests.push({url, ...options}); return handler(url, options); }};
  vm.runInNewContext(fs.readFileSync(path.join(__dirname, '../web/integrations.js'), 'utf8'), context);
  const open = async () => { events['workspace-panel-change']?.({detail:{panel:'connections'}}); await tick(); };
  const click = async id => { nodes.get(id).listeners.click?.({preventDefault(){}}); await tick(); };
  return {nodes, root, document, window, context, requests, logs, timers, open, click};
}

test('connections load on demand and never load the bank widget on page load', async () => {
  const h = await harness();
  assert.equal(h.requests.length, 0);
  assert.equal(h.document.head.children.length, 0);
  await h.open();
  assert.equal(h.requests[0].url, '/api/integrations');
  assert.equal(h.requests[0].credentials, 'same-origin');
  assert.equal(h.document.head.children.length, 0);
  assert.equal(h.requests.filter(request => request.method === 'POST').length, 0);
});

test('disabled and missing credentials remain distinct from a working connection', async () => {
  const h = await harness({handler:() => response({telegram:{enabled:false, configured:true, status:'healthy'}, pluggy:{enabled:true, configured:false, missing_credentials:['private credential']}})});
  await h.open();
  assert.equal(h.nodes.get('telegram-status').textContent, 'Desativado');
  assert.equal(h.nodes.get('pluggy-status').textContent, 'Credenciais incompletas');
  assert.equal(h.nodes.get('pluggy-connect').hidden, true);
  assert.doesNotMatch(copy(h.root), /private credential/);
  assert.doesNotMatch(h.nodes.get('bank-source-label').textContent, /conectad/i);
});

test('configuration alone does not claim that a bank is connected', async () => {
  const h = await harness({handler:() => response({telegram:{enabled:true, configured:true}, pluggy:{enabled:true, configured:true, items:[]}})});
  await h.open();
  assert.equal(h.nodes.get('pluggy-connect').hidden, false);
  assert.equal(h.nodes.get('pluggy-status').textContent, 'Aguardando conexão');
  assert.equal(h.nodes.get('bank-source-label').textContent, 'Conectar instituição');
});

test('uncertain Telegram sends are visible with a recovery that avoids automatic resending', async () => {
  const h = await harness({handler:() => response({telegram:{enabled:true,configured:true,status:'failed',allowed_identity_configured:true,pending:1,queued:2,failed:0,unknown:1}})});
  await h.open();
  assert.match(h.nodes.get('telegram-detail').textContent, /Confira a conversa no Telegram/);
  const health = copy(h.nodes.get('telegram-health'));
  assert.match(health, /Aguardando processamento 1/);
  assert.match(health, /Aguardando envio 2/);
  assert.match(health, /Envios sem confirmação 1/);
  assert.equal(h.requests.filter(request => request.method === 'POST').length, 0);
});

test('setup posts use CSRF and prevent duplicate requests', async () => {
  let finish;
  const h = await harness({handler:(url) => url.endsWith('/setup') ? new Promise(resolve => { finish = resolve; }) : response({telegram:{enabled:true, configured:true},pluggy:{enabled:false}})});
  await h.open();
  await h.click('telegram-setup'); await h.click('telegram-setup');
  const writes = h.requests.filter(request => request.method === 'POST');
  assert.equal(writes.length, 1);
  assert.equal(writes[0].headers['X-CSRF-Token'], 'csrf-private');
  assert.equal(h.nodes.get('telegram-setup').disabled, true);
  finish(response({ok:true})); await tick(); await tick();
  assert.equal(h.nodes.get('telegram-setup').disabled, false);
});

test('forbidden, unavailable and rate limited responses offer specific recovery without raw payloads', async t => {
  for (const [status, expected] of [[403,/sessão|permissão/], [503,/indisponível|configuração/], [429,/limite|aguarde/i]]) {
    await t.test(String(status), async () => {
      const h = await harness({handler:() => response({error:'private-provider-token'},status)}); await h.open();
      assert.match(h.nodes.get('integrations-status').textContent, expected);
      assert.doesNotMatch(copy(h.root), /private-provider-token/);
      assert.equal(h.nodes.get('integrations-refresh').disabled, false);
    });
  }
});

test('a timed out status request unlocks refresh and explains retry', async () => {
  const h = await harness({handler:(_, options) => new Promise((_, reject) => options.signal.addEventListener('abort', () => { const error = new Error('abort'); error.name = 'AbortError'; reject(error); }))});
  await h.open();
  const timeout = [...h.timers.values()].find(timer => timer.delay === 15000); assert.ok(timeout); timeout.callback(); await tick();
  assert.match(h.nodes.get('integrations-status').textContent, /demorou|tempo/i);
  assert.equal(h.nodes.get('integrations-refresh').disabled, false);
});

test('bank records preserve provider decimal strings, show source and never rewrite manual totals', async () => {
  const data = {items:[],accounts:[{id:'acc', name:'Conta <img src=x>', balance:'12345678901234567890.1234', currency:'BRL'}], transactions:[{description:'<script>bad()</script>', amount:'-0.0001',date:'2026-10-28'}], investments:[]};
  const h = await harness({handler:url => response(url.endsWith('/data') ? data : {pluggy:{enabled:true,configured:true,items:[{item_id:'owned',status:'UPDATED'}]}})});
  await h.open(); await h.click('pluggy-data-open');
  const rendered = copy(h.nodes.get('pluggy-data-content'));
  assert.match(rendered, /12345678901234567890\.1234/);
  assert.match(rendered, /-0\.0001/);
  assert.match(rendered, /Pluggy/);
  assert.match(rendered, /<script>bad\(\)<\/script>/);
  assert.equal(descendants(h.nodes.get('pluggy-data-content')).some(node => ['IMG','SCRIPT'].includes(node.tagName)), false);
  assert.equal(h.requests.filter(request => request.method === 'POST').length, 0);
});

test('the widget opens only after an explicit click and registers the returned item with CSRF', async () => {
  const h = await harness({handler:url => response(url.endsWith('/connect-token') ? {accessToken:'connect-private',connectorId:42} : url.endsWith('/items') ? {ok:true} : {pluggy:{enabled:true,configured:true,items:[]}})});
  await h.open(); await h.click('pluggy-connect');
  const script = h.document.head.children[0];
  assert.equal(script.src, 'https://cdn.pluggy.ai/pluggy-connect/v2.8.2/pluggy-connect.js');
  let config;
  h.window.PluggyConnect = class { constructor(options) { config = options; } init() {} };
  script.onload(); await tick(); await tick();
  assert.equal(config.connectToken, 'connect-private');
  assert.equal(config.selectedConnectorId, 42);
  assert.deepEqual(Array.from(config.connectorIds), [42]);
  assert.equal(config.includeSandbox, false);
  assert.deepEqual(Array.from(config.products || []), ['ACCOUNTS', 'CREDIT_CARDS', 'TRANSACTIONS', 'INVESTMENTS']);
  await config.onSuccess({item:{id:'owned-item'}}); await tick();
  const request = h.requests.find(request => request.url === '/api/integrations/pluggy/items');
  assert.equal(request.headers['X-CSRF-Token'], 'csrf-private');
  assert.equal(JSON.parse(request.body).item_id, 'owned-item');
  assert.doesNotMatch(copy(h.root), /connect-private/);
  assert.equal(h.logs.length, 0);
});

test('an invalid connector cannot open an unrestricted widget', async () => {
  const h = await harness({handler:url => response(url.endsWith('/connect-token') ? {accessToken:'private', connectorId:null} : {pluggy:{enabled:true,configured:true}})});
  await h.open(); await h.click('pluggy-connect');
  let opened = false;
  h.window.PluggyConnect = class { constructor() { opened = true; } init() {} };
  h.document.head.children[0].onload(); await tick(); await tick();
  assert.equal(opened, false);
  assert.match(h.nodes.get('integrations-status').textContent, /conector|configuração/);
});

test('a widget error cannot open a second widget before the first closes', async () => {
  const h = await harness({handler:url => response(url.endsWith('/connect-token') ? {accessToken:'private',connectorId:42} : {pluggy:{enabled:true,configured:true,items:[]}})});
  await h.open(); await h.click('pluggy-connect');
  let config, instances = 0;
  h.window.PluggyConnect = class { constructor(options) { config = options; instances++; } init() {} };
  h.document.head.children[0].onload(); await tick(); await tick();
  config.onError({message:'private provider error'}); await h.click('pluggy-connect');
  assert.equal(instances, 1);
  assert.equal(h.nodes.get('pluggy-connect').disabled, true);
  config.onClose(); await h.click('pluggy-connect');
  assert.equal(instances, 2);
  assert.doesNotMatch(copy(h.root), /private provider error/);
});

test('sync only queues the selected owned item and unlocks its replacement button after refresh', async () => {
  let complete;
  const h = await harness({handler:url => url.endsWith('/sync') ? new Promise(resolve => { complete = resolve; }) : response({pluggy:{enabled:true,configured:true,items:[{item_id:'owned-item',status:'UPDATED'}]}})});
  await h.open();
  const oldButton = descendants(h.nodes.get('pluggy-items')).find(node => node.dataset.syncItem);
  h.nodes.get('pluggy-items').listeners.click({target:oldButton});
  h.nodes.get('pluggy-items').listeners.click({target:oldButton}); await tick();
  const writes = h.requests.filter(request => request.method === 'POST');
  assert.equal(writes.length, 1);
  assert.equal(writes[0].url, '/api/integrations/pluggy/sync');
  assert.equal(JSON.parse(writes[0].body).item_id, 'owned-item');
  assert.equal(writes[0].headers['X-CSRF-Token'], 'csrf-private');
  complete(response({ok:true})); await tick(); await tick();
  const newButton = descendants(h.nodes.get('pluggy-items')).find(node => node.dataset.syncItem);
  assert.notEqual(newButton, oldButton);
  assert.equal(newButton.disabled, false);
});

test('provider payload wrappers are displayed without flattening source amounts into manual finance', async () => {
  const h = await harness({handler:url => response(url.endsWith('/data') ? {accounts:[{id:'account',payload:{name:'Minha conta',balance:'0.000001',currencyCode:'BRL'}}]} : {pluggy:{enabled:true,configured:true,items:[{id:'owned-item',status:'UPDATED'}]}})});
  await h.open(); await h.click('pluggy-data-open');
  assert.match(copy(h.nodes.get('pluggy-data-content')), /Minha conta/);
  assert.match(copy(h.nodes.get('pluggy-data-content')), /0\.000001/);
  assert.match(copy(h.nodes.get('pluggy-data-content')), /Não entram nos totais manuais/);
});

test('exact normalized amounts take priority over rounded numbers inside provider payload', async () => {
  const h = await harness({handler:url => response(url.endsWith('/data') ? {accounts:[{balance:'9007199254740993.123456789',currency:'USD',active:false,payload:{balance:9007199254740992,currencyCode:'USD'}}],transactions:[{amount:'-0.123456789123456789',payload:{amount:-0.12345678912345678}}]} : {pluggy:{enabled:true,configured:true,items:[{id:'owned-item'}]}})});
  await h.open(); await h.click('pluggy-data-open');
  const rendered = copy(h.nodes.get('pluggy-data-content'));
  assert.match(rendered, /9007199254740993\.123456789/);
  assert.match(rendered, /-0\.123456789123456789/);
  assert.match(rendered, /Registro anterior, fora da coleta atual/);
});

test('the script is safe on pages without the connections surface', async () => {
  const h = await harness({hasRoot:false});
  assert.equal(h.requests.length, 0);
});

test('superseded connections are separated from current connections and have no sync action', async () => {
  const h = await harness({handler:() => response({pluggy:{enabled:true,configured:true,status:'partial',items:[{item_id:'current',status:'connected'},{item_id:'old',status:'revoked',superseded_by:'current'},{item_id:'needs-consent',status:'revoked'}]}})});
  await h.open();
  assert.match(h.nodes.get('pluggy-status').textContent, /1.*atualizada.*1.*atenção/);
  const items = h.nodes.get('pluggy-items');
  assert.ok(descendants(items).some(node => node.tagName === 'DETAILS'));
  assert.equal(descendants(items).filter(node => node.dataset.syncItem === 'old').length, 0);
  assert.match(copy(items), /Conexões anteriores/);
});

test('stale connections distinguish institution freshness from a recent import and explain recovery', async () => {
  const h = await harness({handler:() => response({pluggy:{enabled:true,configured:true,status:'stale',last_synced_at:'2026-11-14T12:00:00Z',items:[{item_id:'current',status:'stale',last_source_sync:'2026-11-06T12:00:00Z',last_synced_at:'2026-11-14T12:00:00Z'}]}})});
  await h.open();
  const text = copy(h.nodes.get('pluggy-items'));
  assert.match(text, /Atualização da instituição: 06\/11\/2026/);
  assert.match(text, /Importado pelo CONSELHO: 14\/11\/2026/);
  assert.match(text, /MeuPluggy.*Sincronizar dados/);
  assert.match(copy(h.nodes.get('pluggy-health')), /Última importação pelo CONSELHO/);
  assert.ok(h.requests.every(request => !request.method || request.method === 'GET'));
});

test('missing source dates are explicit and are never inferred from the import timestamp', async () => {
  const h = await harness({handler:() => response({pluggy:{enabled:true,configured:true,status:'connected',items:[{item_id:'current',status:'connected',last_synced_at:'2026-11-14T12:00:00Z'}]}})});
  await h.open();
  const text = copy(h.nodes.get('pluggy-items'));
  assert.match(text, /Atualização da instituição: não informada/);
  assert.doesNotMatch(text, /Atualização da instituição: 14\/11/);
  assert.match(text, /Importado pelo CONSELHO: 14\/11\/2026/);
  assert.doesNotMatch(text, /Atualize a conexão no MeuPluggy/);
});
