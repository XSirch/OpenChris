const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const root = path.join(__dirname, '..');
const modelPath = path.join(root, 'web', 'replay-model.js');
const replay = fs.existsSync(modelPath) ? require(modelPath) : {};
const templates = {
  'conversation.received':['pending', 'Conversa recebida'],
  'agent.started':['running', 'Organização iniciada'],
  'memory.episode_saved':['complete', 'Memória registrada'],
  'agent.completed':['idle', 'Organização concluída'],
  'agent.failed':['error', 'Organização interrompida']
};
function event(sequence, eventType, extra = {}) {
  const [state, title] = templates[eventType];
  return {sequence, event_type:eventType, state, source_event_id:42, channel:'dashboard', created_at:'2026-10-26T12:00:00.123456Z', title, ...extra};
}
const timeline = () => [event(3, 'conversation.received'), event(7, 'agent.started', {agent_id:'A02'}), event(8, 'memory.episode_saved'), event(11, 'agent.completed', {agent_id:'A02'})];

test('replay validation returns independent events ordered by journal sequence', () => {
  assert.equal(typeof replay.validateEvents, 'function');
  const input = timeline().reverse();
  const result = replay.validateEvents(input);
  assert.deepEqual(result.map(item => item.sequence), [3, 7, 8, 11]);
  assert.deepEqual(input.map(item => item.sequence), [11, 8, 7, 3]);
  assert.notEqual(result, input);
  assert.notEqual(result[0], input[3]);
  result[0].title = 'changed';
  assert.equal(input[3].title, 'Conversa recebida');
});

test('replay advances and rewinds from the beginning without retaining future work', () => {
  const events = timeline();
  assert.deepEqual(replay.stateAt(events, -1), {states:{}, sequence:null, created_at:null, index:-1});
  assert.deepEqual(replay.stateAt(events, 0).states, {});
  assert.deepEqual(replay.stateAt(events, 1).states, {A02:{state:'running', task:'Organização iniciada'}});
  assert.equal(replay.stateAt(events, 2).states.A02.state, 'running');
  const done = replay.stateAt(events, 3);
  assert.deepEqual(done.states, {A02:{state:'available', task:'Organização concluída'}});
  assert.equal(done.sequence, 11);
  assert.equal(done.created_at, events[3].created_at);
  assert.equal(done.index, 3);
  done.states.A02.state = 'failed';
  assert.equal(replay.stateAt(events, 1).states.A02.state, 'running');
  assert.deepEqual(events, timeline());
});

test('only agent lifecycle events with a recorded identity change character states', () => {
  const events = [event(1, 'conversation.received'), event(2, 'agent.started'), event(3, 'agent.started', {agent_id:'A23'}), event(4, 'agent.failed', {agent_id:'A23'})];
  assert.deepEqual(replay.stateAt(events, 1).states, {});
  assert.deepEqual(replay.stateAt(events, 3).states, {A23:{state:'failed', task:'Organização interrompida'}});
  assert.deepEqual(replay.stateAt(events, 2).states, {A23:{state:'running', task:'Organização iniciada'}});
});

test('validation rejects unsafe journal identity, schema, states, text and mixed executions', () => {
  const invalid = [
    {...event(1, 'conversation.received'), sequence:0},
    {...event(1, 'conversation.received'), sequence:1.2},
    {...event(1, 'conversation.received'), sequence:Number.MAX_SAFE_INTEGER + 1},
    {...event(1, 'conversation.received'), source_event_id:0},
    {...event(1, 'conversation.received'), source_event_id:'42'},
    {...event(1, 'agent.started'), agent_id:'A24'},
    {...event(1, 'conversation.received'), agent_id:'A02'},
    {...event(1, 'agent.started'), agent_id:'__proto__'},
    {...event(1, 'agent.started'), event_type:'agent.invented'},
    {...event(1, 'agent.completed'), state:'running'},
    {...event(1, 'conversation.received'), channel:'email'},
    {...event(1, 'conversation.received'), created_at:'tomorrow'},
    {...event(1, 'conversation.received'), created_at:'2026-02-30T12:00:00Z'},
    {...event(1, 'conversation.received'), created_at:'2026-10-26'},
    {...event(1, 'conversation.received'), title:'<img src=x onerror=alert(1)>'},
    {...event(1, 'conversation.received'), title:'private user message'},
    {...event(1, 'conversation.received'), title:'Conversa recebida\n'},
    {...event(1, 'conversation.received'), payload:{body:'private message'}}
  ];
  for (const item of invalid) assert.throws(() => replay.validateEvents([item]), /replay|evento|sequência|título|agente|canal|data|execução/i);
  assert.throws(() => replay.validateEvents([event(1, 'conversation.received'), event(1, 'agent.started')]), /sequência/i);
  assert.throws(() => replay.validateEvents([event(1, 'conversation.received'), event(2, 'agent.started', {source_event_id:43})]), /execução/i);
  assert.throws(() => replay.validateEvents([event(1, 'conversation.received'), event(2, 'agent.started', {channel:'telegram'})]), /canal|execução/i);
  assert.throws(() => replay.validateEvents(null), /replay|eventos/i);
});

test('replay has a hard limit of 200 events and validates stage boundaries', () => {
  const full = Array.from({length:200}, (_, index) => event(index + 1, 'conversation.received'));
  assert.equal(replay.validateEvents(full).length, 200);
  assert.equal(replay.stateAt(full, 199).sequence, 200);
  assert.throws(() => replay.validateEvents([...full, event(201, 'conversation.received')]), /200/);
  for (const index of [-2, 4, 0.5, NaN]) assert.throws(() => replay.stateAt(timeline(), index), /etapa|índice/i);
  assert.deepEqual(replay.stateAt([], -1).states, {});
  assert.throws(() => replay.stateAt([], 0), /etapa|índice/i);
});

function target() {
  const listeners = new Map();
  return {
    addEventListener(type, callback) { if (!listeners.has(type)) listeners.set(type, []); listeners.get(type).push(callback); },
    dispatchEvent(event) { for (const callback of listeners.get(event.type) || []) callback(event); return true; }
  };
}
function element(id = '', tag = 'div') {
  const attributes = new Map();
  const classes = new Set();
  const item = Object.assign(target(), {
    id, tagName:tag, dataset:{}, children:[], hidden:false, disabled:false, textContent:'', value:'', isConnected:true,
    classList:{toggle(name, value) { if (value) classes.add(name); else classes.delete(name); }, remove(...names) { for (const name of names) classes.delete(name); }},
    append(...children) { this.children.push(...children); }, replaceChildren(...children) { this.children = children.flatMap(child => child.tagName === 'fragment' ? child.children : [child]); },
    setAttribute(name, value) { attributes.set(name, value); }, getAttribute(name) { return attributes.get(name); }, focus() {},
    getBoundingClientRect() { return {left:0, top:0, width:1000, height:750}; }, getClientRects() { return [this.getBoundingClientRect()]; },
    querySelector(selector) { return this.children.find(child => selector === 'canvas' ? child.tagName === 'canvas' : child.className?.split(' ').includes(selector.slice(1))) || this.children.map(child => child.querySelector?.(selector)).find(Boolean) || null; },
    getContext() { return new Proxy({imageSmoothingEnabled:false}, {get(object, key) { return key in object ? object[key] : () => {}; }}); },
    setPointerCapture() {}, hasPointerCapture() { return false; }, releasePointerCapture() {}, closest() { return null; }
  });
  return item;
}
async function officeHarness() {
  const manifest = JSON.parse(fs.readFileSync(path.join(root, 'assets', 'office', 'manifest.json'), 'utf8'));
  const elements = new Map(['office-canvas', 'office-viewport', 'agent-roster', 'agent-panel', 'agent-name', 'agent-state', 'agent-current-task', 'office-selection', 'walk-agent', 'send-agent-home', 'tour-office', 'office-status', 'expand-office'].map(id => [id, element(id)]));
  const document = Object.assign(target(), {hidden:false, body:{dataset:{}}, getElementById:id => elements.get(id) || null, createElement:tag => element('', tag), createDocumentFragment:() => element('', 'fragment'), querySelectorAll:() => []});
  const window = Object.assign(target(), {devicePixelRatio:1, ConselhoOfficeModel:require(path.join(root, 'web', 'office-model.js'))});
  let requests = 0, frames = new Map(), nextFrame = 0;
  const context = {window, document, console, setTimeout:()=>1, clearTimeout(){}, matchMedia:() => ({matches:false, addEventListener() {}}), localStorage:{getItem() { return null; }, setItem() {}},
    requestAnimationFrame(callback) { frames.set(++nextFrame, callback); return nextFrame; }, cancelAnimationFrame(id) { frames.delete(id); },
    CustomEvent:class { constructor(type, init = {}) { this.type = type; this.detail = init.detail; } },
    fetch:async () => { requests++; return {ok:true, json:async () => manifest}; },
    Image:class { set src(url) { const asset = manifest.atlases.find(atlas => atlas.url === url) || manifest.world; this.naturalWidth = asset.width; this.naturalHeight = asset.height; queueMicrotask(() => this.onload()); } }
  };
  vm.runInNewContext(fs.readFileSync(path.join(root, 'web', 'office.js'), 'utf8'), context);
  await new Promise(resolve => setImmediate(resolve));
  return {office:window.ConselhoOffice, window, elements, context, requests:() => requests,
    tick(time) { const current = [...frames.values()]; frames.clear(); for (const callback of current) callback(time); }};
}

test('office replay cancels routes in place and preserves live updates for exit', async () => {
  const harness = await officeHarness();
  const {office, window, elements} = harness;
  assert.equal(office.getState().ready, true, office.getState().error);
  assert.equal(typeof office.setReplay, 'function');
  const modes = [], selections = [];
  window.addEventListener('office-replay-mode', event => modes.push(event.detail.active));
  window.addEventListener('office-agent-selected', event => selections.push(event.detail));
  assert.equal(office.selectAgent('A02'), true);
  assert.equal(selections[0].id, 'A02');
  assert.equal(typeof selections[0].name, 'string');
  elements.get('tour-office').dispatchEvent({type:'click'});
  assert.ok(office.getState().agents.some(agent => agent.path.length));
  harness.tick(100); harness.tick(150);
  const positions = office.getState().agents.map(agent => [agent.id, agent.x, agent.y]);
  assert.equal(office.setReplay(replay.stateAt(timeline(), 1)), true);
  let snapshot = office.getState();
  assert.equal(snapshot.replay.active, true);
  assert.equal(snapshot.replay.sequence, 7);
  assert.equal(snapshot.replay.index, 1);
  assert.deepEqual(snapshot.agents.map(agent => [agent.id, agent.x, agent.y]), positions);
  assert.ok(snapshot.agents.every(agent => !agent.path.length && !agent.target));
  assert.equal(elements.get('tour-office').disabled, true);
  assert.match(elements.get('agent-name').textContent, /Histórico/);
  window.dispatchEvent({type:'office-event', detail:{event_type:'agent.started', payload:{agent_id:'A23', state:'running'}}});
  assert.equal(office.getState().agents.find(agent => agent.id === 'A02').state, 'running');
  assert.notEqual(office.getState().agents.find(agent => agent.id === 'A23').state, 'running');
  assert.equal(office.selectAgent('A23'), true);
  assert.equal(elements.get('agent-current-task').textContent, 'Sem atividade nesta etapa');
  assert.equal(office.walkSelectedTo({x:100,y:100}), false);
  const canvas = elements.get('office-canvas'), beforeZoom = office.getState().camera.scale;
  canvas.dispatchEvent({type:'keydown', key:'+', preventDefault() {}});
  assert.ok(office.getState().camera.scale > beforeZoom, 'camera controls remain available in replay');
  canvas.dispatchEvent({type:'pointerdown', pointerType:'mouse', button:0, pointerId:1, clientX:0, clientY:0});
  canvas.dispatchEvent({type:'pointerup', pointerId:1, clientX:0, clientY:0});
  elements.get('tour-office').dispatchEvent({type:'click'});
  assert.ok(office.getState().agents.every(agent => !agent.path.length && !agent.target));
  const frame = replay.stateAt(timeline(), 3);
  assert.equal(office.setReplay(frame), true);
  frame.states.A02.state = 'running';
  assert.equal(office.getState().agents.find(agent => agent.id === 'A02').state, 'available');
  assert.deepEqual(modes, [true]);
  assert.equal(office.exitReplay(), true);
  snapshot = office.getState();
  assert.equal(snapshot.replay, null);
  assert.equal(snapshot.agents.find(agent => agent.id === 'A23').state, 'running');
  assert.equal(elements.get('tour-office').disabled, false);
  assert.deepEqual(modes, [true, false]);
  assert.equal(harness.requests(), 1, 'replay never fetches or posts work');
});

test('live reconciliation continues during replay and malformed frames do not enter it', async () => {
  const {office, window} = await officeHarness();
  assert.equal(office.setReplay({states:{A99:{state:'running', task:'private'}}, index:0, sequence:1, created_at:'2026-10-26T12:00:00Z'}), false);
  assert.equal(office.getState().replay, null);
  window.dispatchEvent({type:'office-event', detail:{event_type:'agent.started', payload:{agent_id:'A02', state:'running'}}});
  office.setReplay(replay.stateAt(timeline(), 1));
  window.dispatchEvent({type:'conselho-state', detail:{snapshot:{pending:0}}});
  assert.equal(office.getState().agents.find(agent => agent.id === 'A02').state, 'running');
  office.exitReplay();
  assert.equal(office.getState().agents.find(agent => agent.id === 'A02').state, 'available');
  assert.equal(office.exitReplay(), false);
});


test('canvas hover, drag and object clicks share contextual targets without starting work', async () => {
  const harness = await officeHarness();
  const {office, window, elements} = harness;
  const canvas = elements.get('office-canvas'), model = window.ConselhoOfficeModel;
  const manifest = JSON.parse(fs.readFileSync(path.join(root, 'assets', 'office', 'manifest.json'), 'utf8'));
  const opened = [];
  window.ConselhoWorkspace = {openPanel:(name, source) => opened.push({name, source})};
  function pointer(type, point, extra = {}) {
    canvas.dispatchEvent({type, pointerType:'mouse', button:0, pointerId:1, clientX:point.x, clientY:point.y, ...extra});
  }
  function screen(p) { return model.worldToScreen(p, office.getState().camera); }
  const agent = office.getState().agents.find(agent => agent.id === 'A02');
  const torso = screen({x:agent.x, y:agent.y-60});
  pointer('pointermove', torso);
  assert.equal(canvas.dataset.cursor, 'pointer');
  assert.match(canvas.title, /Mnema/);
  pointer('pointerdown', torso);
  assert.equal(canvas.dataset.cursor, 'pointer', 'press alone is not a drag');
  pointer('pointermove', {x:torso.x+30,y:torso.y});
  assert.equal(canvas.dataset.cursor, 'grabbing');
  pointer('pointerup', {x:torso.x+30,y:torso.y});
  assert.equal(office.getState().selected, null, 'panning must not select a character');
  pointer('pointermove', screen({x:1520,y:50}));
  assert.equal(canvas.dataset.cursor, 'grab');
  assert.equal(canvas.title, '');
  const object = manifest.world.interactives.find(object => object.action.target === 'finance');
  const p = screen({x:object.polygon.reduce((n,p) => n+p[0],0)/object.polygon.length,y:object.polygon.reduce((n,p) => n+p[1],0)/object.polygon.length});
  pointer('pointermove', p);
  assert.equal(canvas.dataset.cursor, 'pointer');
  assert.match(canvas.title, /financeiro/i);
  pointer('pointerdown', p); pointer('pointerup', p);
  assert.equal(opened[0].name, 'finance');
  assert.equal(opened[0].source, canvas);
  assert.ok(office.getState().agents.every(agent => !agent.path.length && !agent.target));
  assert.equal(harness.requests(), 1);
  pointer('pointerleave', p);
  assert.equal(canvas.dataset.cursor, 'grab');
  assert.equal(canvas.title, '');
  pointer('pointermove', p); pointer('pointerdown', p); pointer('pointercancel', p);
  assert.equal(canvas.dataset.cursor, 'grab');
  assert.equal(opened.length, 1, 'canceling must not click the object');
  pointer('pointermove', p, {pointerType:'touch'});
  assert.equal(canvas.dataset.cursor, 'grab', 'touch must not leave a mouse hover behind');
});


test('object navigation leaves expanded mode so the destination stays visible', async () => {
  const {office, window, elements} = await officeHarness();
  const canvas = elements.get('office-canvas'), viewport = elements.get('office-viewport');
  const manifest = JSON.parse(fs.readFileSync(path.join(root, 'assets', 'office', 'manifest.json'), 'utf8'));
  window.ConselhoWorkspace = {openPanel(name) {
    assert.equal(name, 'goals');
    assert.equal(viewport.dataset.expanded, 'false', 'collapse before focusing the sidebar');
  }};
  elements.get('expand-office').dispatchEvent({type:'click'});
  assert.equal(viewport.dataset.expanded, 'true');
  const object = manifest.world.interactives.find(object => object.action.target === 'goals');
  const p = window.ConselhoOfficeModel.worldToScreen({x:object.polygon.reduce((n,p) => n+p[0],0)/object.polygon.length,y:object.polygon.reduce((n,p) => n+p[1],0)/object.polygon.length},office.getState().camera);
  for (const type of ['pointerdown','pointerup']) canvas.dispatchEvent({type, pointerType:'mouse', button:0, pointerId:1, clientX:p.x, clientY:p.y});
  assert.equal(viewport.dataset.expanded, 'false');
  assert.equal(elements.get('expand-office').getAttribute('aria-pressed'), 'false');
});

test('opening the financial panel from the scene controls leaves expanded mode', async () => {
  const {window,elements}=await officeHarness();
  elements.get('expand-office').dispatchEvent({type:'click'});
  assert.equal(elements.get('office-viewport').dataset.expanded,'true');
  window.dispatchEvent({type:'workspace-panel-change',detail:{panel:'finance'}});
  assert.equal(elements.get('office-viewport').dataset.expanded,'false');
});
