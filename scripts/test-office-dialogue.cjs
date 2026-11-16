const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const file = require('node:path').join(__dirname, '../web/office-dialogue-model.js');
const model = fs.existsSync(file) ? require(file) : {};
const id = '00000000-0000-4000-8000-000000000099';
const at = '2026-11-08T12:00:00Z';
function detail() { return {run:{id,status:'await_data',created_at:at},tasks:[
 {id:'task-risk',run_id:id,stage:2,agent_id:'A17',status:'complete',completed_at:at,result:{reply:'O risco depende da liquidez. A hipótese precisa de revisão.'}},
 {id:'task-strategy',run_id:id,stage:1,agent_id:'A11',status:'complete',completed_at:at,result:{reply:'Proponho estudar a composição. Falta confirmar o horizonte.'}},
 {id:'task-search',run_id:id,stage:0,agent_id:'A13',status:'complete',completed_at:at,result:{summary:'A pesquisa consultou fontes públicas.'}},
 {id:'task-critic',run_id:id,stage:3,agent_id:'A21',status:'pending',result:null}
]}; }

test('dialogue uses persisted reports in stage order and the actual responsible agent',()=>{
 assert.equal(typeof model.project,'function'); const p=model.project(detail(),{A11:"Bento"});
 assert.equal(p.runId,id); assert.equal(p.historical,true);
 assert.deepEqual(p.messages.map(m=>m.agentId),['A13','A11','A17']);
 assert.equal(p.messages[1].name,'Bento');
 assert.match(p.messages[1].text,/Proponho estudar/);
 assert.equal(p.messages[1].href,`/reports?run=${id}#task-1`);
 assert.equal(p.messages[1].at,at);
});
test('pending tasks, failed results and arbitrary speaker IDs never become speech',()=>{
 const d=detail();d.tasks[0].status='failed_unknown';d.tasks[1].agent_id='A99';d.tasks[2].result=null;
 assert.equal(typeof model.project,'function');assert.deepEqual(model.project(d).messages,[]);
});
test('an active signal requires a running persisted task and history never claims to be live',()=>{
 assert.equal(typeof model.project,'function');const d=detail();d.run.status='running';d.tasks[3].status='running';d.tasks[3].started_at=at;
 assert.equal(model.project(d).active.agentId,'A21');
 d.tasks[3].status='pending';assert.equal(model.project(d).active,null);
 d.run.status='await_data';assert.equal(model.project(d).historical,true);
});
test('untrusted report text remains literal and UTF-8 excerpts preserve accents and codepoints',()=>{
 assert.equal(typeof model.excerpt,'function');const raw='<img onerror=alert(1)> Estratégia e revisão 🧭 '.repeat(20);
 const clipped=model.excerpt(raw,100);assert.ok(Array.from(clipped).length<=101);
 assert.match(clipped,/<img onerror/);assert.match(clipped,/Estratégia e revisão/);
 assert.ok(!clipped.includes('\uFFFD'));assert.equal(model.excerpt('  linha\n seguinte  '),'linha seguinte');
});
test('empty or malformed rounds do not create imaginary dialogue',()=>{
 assert.equal(typeof model.project,'function');assert.equal(model.project(null),null);
 assert.equal(model.project({run:{id:'javascript:alert(1)'},tasks:[]}),null);
 const d=detail();d.tasks=[];assert.deepEqual(model.project(d).messages,[]);
});
test('bubble layout stays inside the viewport and hides an offscreen speaker',()=>{
 assert.equal(typeof model.place,'function');const p=model.place({x:380,y:50},{width:390,height:440},230,140);
 assert.ok(p.left>=8);assert.ok(p.left+230<=382);assert.ok(p.top>=8);assert.ok(p.top+140<=376);
 assert.equal(model.place({x:-30,y:50},{width:390,height:440},230,140),null);
});
test('stale heartbeat or disabled supervisor cannot keep a character processing',()=>{
 assert.equal(typeof model.runningTasks,'function');const status={enabled:true,heartbeat_at:at,runs:[{id,status:'running'}],tasks:[{run_id:id,stage:2,agent_id:'A17',status:'running',lease_expires_at:'2026-11-08T12:04:00Z'},{run_id:id,stage:3,agent_id:'A21',status:'pending'}]};
 assert.equal(model.runningTasks(status,Date.parse(at)+60000).length,1);
 assert.equal(model.runningTasks(status,Date.parse(at)+180001).length,0);
 assert.equal(model.runningTasks({...status,enabled:false},Date.parse(at)).length,0);
});

test('live projection seats the responsible agent and the authors of the reviewed opinions',()=>{
 const d = detail();
 d.run.status = 'running';
 const lease = '2026-11-08T12:04:00Z';
 const tasks = ['A13','A08','A17','A21','A01'].map((agent_id,stage)=>({agent_id,stage,id:'task-'+stage,run_id:id,lease_expires_at:lease}));
 for (const [stage,expected] of [[0,[]],[1,[]],[2,['A08']],[3,['A08']],[4,['A08','A17','A21']]]) {
  const status = {enabled:true,heartbeat_at:at,runs:[d.run],tasks:tasks.map(t=>({...t,status:t.stage===stage?'running':t.stage<stage?'complete':'pending'}))};
  const state = model.activities(status,[],Date.parse(at));
  assert.deepEqual(Object.keys(state).filter(id=>state[id].state==='review'),expected);
  assert.equal(state[tasks[stage].agent_id].seat,stage<2?'workstation':'meeting');
 }
 d.tasks.find(task=>task.stage===1).agent_id='A08';
 assert.equal(model.project(d,{A08:'Davi'}).messages[1].name,'Davi');
});

test('pause preserves real execution until its lease expires, and a manual queue cannot hide it',()=>{
 const status = {enabled:true,paused:true,heartbeat_at:at,runs:[{id,status:'running'}],tasks:[{run_id:id,stage:1,id:'proposal',agent_id:'A08',status:'running',lease_expires_at:'2026-11-08T12:01:00Z'}]};
 const queue = [{agent_id:'A08',status:'pending'}];
 const running = model.activities(status,queue,Date.parse(at)).A08;
 assert.match(running.task,/resultado.*descartado/);
 assert.equal(running.seat,'workstation');
 assert.equal(model.activities(status,[],Date.parse(at)+60001).A08.state,'unconfirmed');
});
