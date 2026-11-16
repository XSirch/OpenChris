const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');

const modelPath = path.join(__dirname, '..', 'web', 'office-model.js');
const office = fs.existsSync(modelPath) ? require(modelPath) : {};
const square = (x, y, width, height) => [[x, y], [x + width, y], [x + width, y + height], [x, y + height]];
const world = (obstacles = []) => ({width:240, height:240, walkable:square(0, 0, 240, 240), obstacles});
function manifest() {
  return {
    version:1,
    world:{...world(), background:'/static/office/background.png', rooms:{memory:{x:36,y:36}, goals:{x:204,y:36}, council:{x:108,y:132}, legal:{x:204,y:204}}},
    atlases:[{id:'a', url:'/static/office/agents-a.png', width:1024, height:1024}],
    agents:Array.from({length:23}, (_, index) => ({
      id:'A' + String(index + 1).padStart(2, '0'), name:'Pessoa ' + (index + 1), role:'Papel', description:'Descrição.',
      atlas:'a', height:90, home:{x:36 + index % 8 * 24, y:36 + Math.floor(index / 8) * 48},
      frames:Array.from({length:3}, (_, pose) => ({x:pose * 32, y:index * 40, w:32, h:40}))
    }))
  };
}

test('the office model is available in CommonJS and validates all 23 identities', () => {
  assert.equal(typeof office.validateManifest, 'function');
  const result = office.validateManifest(manifest());
  assert.equal(result.agents.length, 23);
  assert.equal(result.agents[22].id, 'A23');
  assert.equal(new Set(result.agents.map(agent => agent.id)).size, 23);
});

test('manifest rejects missing identities, duplicate sprites and invalid atlas bounds', () => {
  const missing = manifest(); missing.agents.pop();
  assert.throws(() => office.validateManifest(missing), /23/);
  const duplicate = manifest(); duplicate.agents[22].id = 'A01';
  assert.throws(() => office.validateManifest(duplicate), /identidade|ID/);
  const shared = manifest(); shared.agents[22].frames = shared.agents[0].frames;
  assert.throws(() => office.validateManifest(shared), /quadro|sprite/);
  const outOfBounds = manifest(); outOfBounds.agents[22].frames[2].x = 1020;
  assert.throws(() => office.validateManifest(outOfBounds), /atlas/);
});

test('assets stay local and manifest text cannot create markup', () => {
  const external = manifest(); external.atlases[0].url = 'https://example.org/sprites.png';
  assert.throws(() => office.validateManifest(external), /local/);
  const unsafe = manifest(); unsafe.agents[0].name = '<img src=x onerror=alert(1)>';
  const parsed = office.validateManifest(unsafe);
  assert.equal(parsed.agents[0].name, unsafe.agents[0].name);
  assert.notEqual(parsed.agents, unsafe.agents);
});

test('A* follows the 24 pixel grid around a furniture obstacle', () => {
  const grid = office.createGrid(world([square(96, 48, 48, 144)]), 24);
  const route = office.findPath(grid, {x:36,y:132}, {x:204,y:132});
  assert.ok(route.length > 8, 'the route must go around the furniture');
  for (const point of route) {
    assert.equal(office.isWalkable(grid, point), true);
    assert.equal(office.pointInPolygon(point, square(96, 48, 48, 144)), false);
  }
  assert.deepEqual(route.at(-1), {x:204,y:132});
});

test('a solid partition, the exterior and furniture cannot be destinations', () => {
  const grid = office.createGrid(world([square(96, 0, 48, 240)]), 24);
  assert.deepEqual(office.findPath(grid, {x:36,y:132}, {x:204,y:132}), []);
  assert.deepEqual(office.findPath(grid, {x:36,y:132}, {x:110,y:132}), []);
  assert.deepEqual(office.findPath(grid, {x:36,y:132}, {x:-1,y:132}), []);
});

test('diagonal paths never cut a blocked corner', () => {
  const grid = office.createGrid(world([square(24, 0, 24, 24), square(0, 24, 24, 24)]), 24);
  assert.deepEqual(office.findPath(grid, {x:12,y:12}, {x:60,y:60}), []);
});

test('occupied feet and reserved destinations block routes and nearest allocation', () => {
  const grid = office.createGrid(world(), 24), occupied = [{x:108,y:108}];
  assert.deepEqual(office.findPath(grid, {x:36,y:108}, {x:108,y:108}, occupied), []);
  const free = office.nearestWalkable(grid, {x:108,y:108}, occupied);
  assert.ok(free);
  assert.ok(Math.hypot(free.x - 108, free.y - 108) >= 24);
  const route = office.findPath(grid, {x:36,y:108}, {x:204,y:108}, occupied);
  assert.ok(route.length);
  assert.ok(route.every(point => Math.hypot(point.x - 108, point.y - 108) >= 24));
});

test('an exact floor destination cannot overlap the neighboring occupied cell', () => {
  const grid = office.createGrid(world(),24);
  assert.deepEqual(office.findPath(grid,{x:36,y:108},{x:95,y:108},[{x:108,y:108}]),[]);
});

test('a narrow diagonal furniture edge blocks a cell even between the sample points', () => {
  const obstacle = [[0,8],[240,104],[240,107],[0,11]];
  const grid = office.createGrid(world([obstacle]),24);
  assert.equal(office.isWalkable(grid,{x:36,y:36}),false);
});

test('fitted camera centers the world and clamped zoom cannot reveal its exterior', () => {
  const fitted = office.fitCamera({width:1536,height:1024}, {width:1000,height:750});
  assert.equal(fitted.x, 0);
  assert.ok(fitted.y > 0);
  const zoomed = office.clampCamera({...fitted, scale:fitted.scale * 2, x:5000, y:-5000}, {width:1536,height:1024}, {width:1000,height:750});
  assert.equal(zoomed.x, 0);
  assert.equal(zoomed.y, 750 - 1024 * zoomed.scale);
  const point = {x:1300,y:900};
  const restored = office.screenToWorld(office.worldToScreen(point, zoomed), zoomed);
  assert.ok(Math.abs(restored.x - point.x) < 0.001 && Math.abs(restored.y - point.y) < 0.001);
});

test('zoom preserves its anchor until the world boundary clamps the camera', () => {
  const dimensions = {width:1536,height:1024}, viewport = {width:1000,height:750};
  const initial = office.fitCamera(dimensions, viewport), anchor = {x:500,y:375};
  const before = office.screenToWorld(anchor, initial);
  const after = office.zoomCamera(initial, 2, anchor, dimensions, viewport);
  assert.deepEqual(office.screenToWorld(anchor, after), before);
  assert.ok(office.zoomCamera(initial, 100, anchor, dimensions, viewport).scale <= initial.scale * 4);
});

test('walking uses distinct left, neutral and right poses with feet anchored', () => {
  const agent = manifest().agents[22];
  assert.deepEqual([0,130,260,390,520].map(time => office.animationFrame(agent, time, true).index), [1,0,2,0,1]);
  assert.equal(office.animationFrame(agent, 260, false).index, 0);
  const rect = office.spriteBounds({...agent, x:120,y:180}, agent.frames[0]);
  assert.equal(rect.y + rect.height, 180);
  assert.equal(rect.x + rect.width / 2, 120);
});

test('selection chooses the frontmost full-body sprite, including A23', () => {
  const rear = {...manifest().agents[0], x:120,y:170};
  const front = {...manifest().agents[22], x:120,y:190};
  assert.equal(office.hitTest([rear,front], {x:120,y:140}).id, 'A23');
  assert.equal(office.hitTest([rear,front], {x:0,y:0}), null);
});

test('only known backend events can mark a character as running', () => {
  const ids = manifest().agents.map(agent => agent.id);
  const started = office.reduceWorkState({}, {event_type:'agent.started',payload:{agent_id:'A01',state:'running'}}, ids);
  assert.equal(started.A01.state, 'running');
  assert.equal(started.A01.task, 'Processando evento registrado');
  assert.deepEqual(office.reduceWorkState(started, {event_type:'<script>',payload:{agent_id:'A23',state:'running'}}, ids), started);
  assert.deepEqual(office.reduceWorkState(started, {event_type:'agent.started',payload:{agent_id:'A99',state:'running'}}, ids), started);
  const complete = office.reduceWorkState(started, {event_type:'agent.completed',payload:{agent_id:'A01',state:'complete'}}, ids);
  assert.equal(complete.A01.state, 'available');
  assert.equal(complete.A01.task, 'Nenhuma tarefa em execução');
});

test('a resumed snapshot with no pending work clears stale running states', () => {
  const active = {A01:{state:'running',task:'Processando evento registrado'}};
  assert.deepEqual(office.reconcileWorkState(active, {pending:1}), active);
  assert.equal(office.reconcileWorkState(active, {pending:0}).A01.state, 'available');
  assert.equal(active.A01.state, 'running', 'reconciliation must not mutate the prior snapshot');
});


test('interactive objects validate local actions and map bounds', () => {
  const source = manifest();
  source.world.interactives = [{id:'memory-screen', label:'Abrir memória', polygon:square(10, 10, 30, 30), action:{type:'panel', target:'memory'}}];
  assert.equal(office.validateManifest(source).world.interactives[0].id, 'memory-screen');
  const invalid = structuredClone(source); invalid.world.interactives[0].action.target = 'https://example.org';
  assert.throws(() => office.validateManifest(invalid), /interativ/i);
  const outside = structuredClone(source); outside.world.interactives[0].polygon[0][0] = -1;
  assert.throws(() => office.validateManifest(outside), /interativ/i);
  const duplicate = structuredClone(source); duplicate.world.interactives.push(structuredClone(duplicate.world.interactives[0]));
  assert.throws(() => office.validateManifest(duplicate), /interativ/i);
});

test('hover and click resolve the same frontmost character before an object', () => {
  const agent = {...manifest().agents[0], x:80, y:100};
  const front = {...manifest().agents[1], x:80, y:110};
  const object = {id:'screen', polygon:square(10, 10, 120, 120)};
  assert.equal(office.hitTarget([agent, front], [object], {x:80,y:70}).agent.id, front.id);
  assert.equal(office.hitTarget([agent], [object], {x:20,y:20}).object.id, object.id);
  assert.equal(office.hitTarget([agent], [object], {x:200,y:200}), null);
  const camera = {x:-400, y:20, scale:2.75};
  const screen = office.worldToScreen({x:80,y:70}, camera);
  assert.equal(office.hitTarget([agent], [object], office.screenToWorld(screen, camera)).agent.id, agent.id);
});

test('seats validate physical chair locations, unique identities and seated sprite frames', () => {
  const source = manifest();
  source.world.seats = [{id:'desk-1',label:'Estação 1',position:{x:80,y:80},approach:{x:60,y:108},facing:1,occluders:[square(60,60,20,20)]}];
  source.agents[0].seated = {atlas:'a',frame:{x:96,y:0,w:32,h:40},height:64};
  assert.equal(office.validateManifest(source).agents[0].seated.height,64);
  const duplicate = structuredClone(source); duplicate.world.seats.push(structuredClone(duplicate.world.seats[0]));
  assert.throws(() => office.validateManifest(duplicate),/assento|cadeira/i);
  const broken = structuredClone(source); broken.agents[0].seated.frame.x = 1023;
  assert.throws(() => office.validateManifest(broken),/sentad|atlas/i);
});

test('limited chair allocation is exclusive, stable and prioritizes actual running agents', () => {
  const seats = [{id:'left'},{id:'right'}];
  const people = [{id:'A01',seatId:'left'},{id:'A02',seatId:'right'},{id:'A23'}];
  assert.deepEqual(office.allocateSeats(people,seats,{}),{A01:'left',A02:'right'});
  const priority = office.allocateSeats(people,seats,{A23:{state:'running'}});
  assert.equal(priority.A23,'left');
  assert.equal(new Set(Object.values(priority)).size,2);
  assert.equal(Object.keys(priority).length,2);
  assert.deepEqual(people,[{id:'A01',seatId:'left'},{id:'A02',seatId:'right'},{id:'A23'}]);
});

test('seated bodies have their own uncompressed bounds and remain clickable', () => {
  const person = {...manifest().agents[0],x:120,y:180,pose:'seated',seated:{atlas:'a',frame:{x:96,y:0,w:50,h:60},height:64}};
  const frame = office.animationFrame(person,0,false);
  assert.equal(frame.atlas,'a');
  assert.equal(frame.pose,'seated');
  assert.deepEqual(frame.rect,person.seated.frame);
  const bounds = office.spriteBounds(person,frame.rect);
  assert.equal(bounds.height,64);
  assert.equal(bounds.width,64*50/60);
  assert.equal(office.hitTest([person],{x:120,y:120}),person);
  assert.equal(office.hitTest([person],{x:120,y:100}),null);
  assert.equal(office.animationFrame(person,130,true).pose,'standing','a walking person must use a standing/walking frame');
});

const seatingHarness = require('../docs/design/office-seating-harness.cjs');
test('every physical seat has a short furniture-free approach connected to the shared council floor', () => {
  const source=office.validateManifest(JSON.parse(fs.readFileSync(path.join(__dirname,'../assets/office/manifest.json'),'utf8')));
  const grid=office.createGrid(source.world),shared=office.nearestWalkable(grid,source.world.rooms.council);
  for(const seat of source.world.seats) {
    assert.equal(office.isWalkable(grid,seat.approach),true,seat.id+' approach must be actual walkable floor');
    assert.ok(Math.hypot(seat.position.x-seat.approach.x,seat.position.y-seat.approach.y)<=72,seat.id+' must not teleport between the floor and chair');
    const route=office.findPath(grid,shared,seat.approach);
    assert.ok(route.length,seat.id+' must connect to the shared floor');
    for(let index=1;index<route.length;index++) for(let fraction=0;fraction<=1;fraction+=0.1) {
      const p={x:route[index-1].x+(route[index].x-route[index-1].x)*fraction,y:route[index-1].y+(route[index].y-route[index-1].y)*fraction};
      assert.equal(office.isWalkable(grid,p),true,seat.id+' route must stay clear of furniture');
    }
  }
});

test('seated drawing anchors the hips to the chair instead of anchoring the feet', () => {
  const person = {...manifest().agents[0],x:650,y:245,pose:'seated',seatAnchor:{x:635,y:204},
    seated:{atlas:'a',frame:{x:0,y:0,w:150,h:240},height:110,anchor:{x:0.4,y:0.7}}};
  const bounds = office.spriteBounds(person,person.seated.frame);
  assert.equal(bounds.x+bounds.width*0.4,635);
  assert.equal(bounds.y+bounds.height*0.7,204);
  assert.equal(office.hitTest([person],{x:bounds.x+2,y:bounds.y+2}),person);
  person.facing=-1;
  const mirrored=office.spriteBounds(person,person.seated.frame);
  assert.equal(mirrored.x+mirrored.width*0.6,635,'mirroring must preserve the anatomical chair anchor');
});

test('manifest rejects anatomical anchors outside the seated body', () => {
  const source=manifest();
  source.agents[0].seated={atlas:'a',frame:{x:96,y:0,w:32,h:40},height:64,anchor:{x:1.5,y:0.7}};
  assert.throws(()=>office.validateManifest(source),/apoio|âncora/i);
});

test('a financial wall screen opens the financial panel', () => {
  const source=manifest();
  source.world.interactives=[{id:'finance-screen',label:'Ver painel financeiro',polygon:square(10,10,30,30),action:{type:'panel',target:'finance'}}];
  assert.equal(office.validateManifest(source).world.interactives[0].action.target,'finance');
});

test('live financial aggregates are painted on the wall even while movement is paused', async () => {
  const h=await seatingHarness({paused:true});
  h.window.dispatchEvent({type:'finance-summary',detail:{mode:'manual',month_label:'outubro de 2026',net_worth_text:'R$ 75.000,00',expense_text:'R$ 123,45',pending_text:'R$ 40,00',top_category:'Moradia',top_category_text:'R$ 123,45',activity_text:'Nenhuma tarefa pendente'}});
  h.tick(100);
  const painted=h.draws.filter(d=>d.type==='fillText').map(d=>d.text);
  for(const value of ['R$ 75.000,00','R$ 123,45','R$ 40,00','Moradia']) assert.ok(painted.includes(value),'wall must display '+value);
  assert.equal(h.office.getState().renderLoopActive,false);
  assert.equal(h.requests(),1,'painting finances must not start extra jobs or polling');
});

test('initial framing keeps the whole financial wall screen visible in a short viewport', async () => {
  const h=await seatingHarness({viewport:{width:840,height:400}}),camera=h.office.getState().camera;
  const screen=h.manifest.world.financeScreen;
  for(const p of [{x:screen.x,y:screen.y},{x:screen.x+screen.width,y:screen.y+screen.width*screen.shearY}]) {
    const q=office.worldToScreen(p,camera);
    assert.ok(q.x>=0 && q.y>=0 && q.x<=840 && q.y<=400,'initial view must not crop the wall monitor');
  }
});


test('meeting seats select a front or rear seated view while retaining anatomical anchors', () => {
  const person={...manifest().agents[0],x:900,y:600,pose:'seated',seatPose:'meeting-front',seatAnchor:{x:880,y:550},facing:-1,
    seated:{atlas:'a',frame:{x:0,y:0,w:30,h:40},height:60,variants:{'meeting-front':{atlas:'front',frame:{x:12,y:24,w:150,h:240},height:110,anchor:{x:0.35,y:0.7}}}}};
  const frame=office.animationFrame(person,0,false);
  assert.equal(frame.atlas,'front');
  const bounds=office.spriteBounds(person,frame.rect);
  assert.equal(bounds.x+bounds.width*0.65,880);
  assert.equal(bounds.y+bounds.height*0.7,550);
  assert.equal(office.animationFrame(person,130,true).atlas,'a','walking must still use the original walking atlas');
});

test('visible meeting legs remain clickable while the table hides the lower body behind its surface', () => {
  const m=JSON.parse(fs.readFileSync(path.join(__dirname,'../assets/office/manifest.json'),'utf8'));
  const source=m.agents.find(a=>a.id==='A06'),seat=m.world.seats.find(s=>s.id==='council-southwest');
  const person={...source,x:seat.position.x,y:seat.position.y,pose:'seated',seatKind:'meeting',seatPose:seat.pose,seatAnchor:seat.anchor,seatMask:m.world.meetingTable.polygon,facing:seat.facing};
  const b=office.spriteBounds(person,office.animationFrame(person,0,false).rect);
  const visible={x:b.x+b.width*0.80,y:b.y+b.height*0.84};
  assert.equal(office.pointInPolygon(visible,person.seatMask),false);
  assert.equal(office.hitTest([person],visible),person,'a visible foot must select its character');
  const farSeat=m.world.seats.find(s=>s.id==='council-west');
  const far={...m.agents.find(a=>a.id==='A10'),x:farSeat.position.x,y:farSeat.position.y,pose:'seated',seatKind:'meeting',seatPose:farSeat.pose,seatAnchor:farSeat.anchor,seatMask:m.world.meetingTable.polygon,facing:farSeat.facing};
  const hidden={x:884,y:484};
  assert.equal(office.pointInPolygon(hidden,far.seatMask),true);
  assert.equal(office.hitTest([far],hidden),null,'the tabletop must not select an invisible leg');
});

function activity(h,detail) {
 h.window.dispatchEvent({type:'office-activity',detail});
}
const individual = {state:'running',seat:'workstation',task:'Preparando a proposta'};
const reviewing = {state:'review',seat:'meeting',task:'Proposta em revisão'};
test('without work the seats stay empty while idle characters walk and change frames',async()=>{
 const h = await seatingHarness({production:true});
 const before = h.office.getState();
 assert.equal(before.agents.filter(a=>a.seatId).length,0);
 h.fireTimers();
 const mover = h.office.getState().agents.find(a=>a.roaming);
 assert.ok(mover?.path.length);
 const frames = new Set();
 for (let t=16;t<=600;t+=16) { h.tick(t); frames.add(h.office.getState().agents.find(a=>a.id===mover.id).frame); }
 const after = h.office.getState().agents.find(a=>a.id===mover.id);
 assert.notDeepEqual([mover.x,mover.y],[after.x,after.y]);
 assert.ok(frames.size>1);
 assert.equal(after.state,'available');
 assert.equal(h.requests(),1);
});

test('only active work and review occupy their assigned seats, even with reduced motion',async()=>{
 const h = await seatingHarness({reduced:true,production:true});
 const get = id => h.office.getState().agents.find(a=>a.id===id);
 activity(h,{A08:individual,A01:{state:'queued',task:'Aguardando cota'}});
 assert.equal(get('A08').seatKind,'workstation');
 assert.equal(get('A01').seatId,null);
 activity(h,{A17:{...reviewing,state:'running'},A08:reviewing});
 assert.equal(get('A08').seatKind,'meeting');
 assert.equal(get('A17').seatKind,'meeting');
 assert.notEqual(get('A08').seatId,get('A17').seatId);
 assert.equal(get('A08').state,'review');
 activity(h,{});
 assert.ok(h.office.getState().agents.every(a=>!a.seatId && !a.seatTarget));
});

test('cancelling during a walk frees the reservation and prevents a late arrival',async()=>{
 const h = await seatingHarness({production:true});
 activity(h,{A08:individual});
 assert.ok(h.office.getState().agents.find(a=>a.id==='A08').seatTarget);
 activity(h,{A08:{state:'unconfirmed',task:'Execução sem confirmação'}});
 for(let t=16;t<=6000;t+=16)h.tick(t);
 assert.ok(h.office.getState().agents.every(a=>!a.seatId && !a.seatTarget));
});

test('paused, hidden and reduced motion suspend idle walks',async()=>{
 for(const options of [{paused:true},{reduced:true},{}]){
  const h = await seatingHarness(options);
  if (!options.paused && !options.reduced) {
   h.document.hidden = true;
   h.document.dispatchEvent({type:'visibilitychange'});
  }
  h.fireTimers();
  assert.ok(h.office.getState().agents.every(a=>!a.path.length));
 }
});
