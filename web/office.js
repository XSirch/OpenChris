(() => {
  'use strict';
  const $ = id => document.getElementById(id);
  const production = document.body.dataset.production === 'true';
  const codexEnabled = document.body.dataset.codexEnabled === 'true';
  const canvas = $('office-canvas'), viewport = $('office-viewport'), model = window.ConselhoOfficeModel, display = window.ConselhoOfficeDisplay;
  if (!canvas || !viewport || !model) return;
  const ctx = canvas.getContext('2d', {alpha:false});
  const reduced = matchMedia('(prefers-reduced-motion: reduce)');
  const ids = Array.from({length:23}, (_, index) => 'A' + String(index + 1).padStart(2,'0'));
  const roomNames = {memory:'Memória',goals:'Metas',council:'Conselho',legal:'Jurídico'};
  const images = new Map(), rosterButtons = new Map(), pointers = new Map();
  let manifest = null, grid = null, agents = [], selected = null, trigger = null, work = {}, lastSnapshot = null, replayFrame = null;
  let camera = null, size = {width:1,height:1}, dpr = 1, paused = false, inView = true, ready = false;
  let raf = 0, previousTime = 0, frameCount = 0, generation = 0, errorText = '', drag = null, pinch = null, hover = null;
  const initialFinance = $('office-finance-panel')?.dataset;
  let finance = display?.project(initialFinance ? {mode:'manual',net_worth_text:initialFinance.wealth,pluggy_wealth:initialFinance.wealthStatus ? {net:initialFinance.wealth,status:initialFinance.wealthStatus} : null,expense_text:initialFinance.expenses,pending_text:initialFinance.pending,top_category:initialFinance.category,top_category_text:initialFinance.categoryAmount,activity_text:initialFinance.activity,month_label:initialFinance.month} : null);
  let financeConnection = 'connecting';
  let routineIndex = 0, routineTimer, activity = {};
  try { paused = localStorage.getItem('conselho-ambient-paused') === 'true'; } catch {}

  const setText = (id,text) => { const element = $(id); if (element) element.textContent = text; };
  const announce = text => setText('office-status',text);
  const append = (tag, text, className) => {
    const element = document.createElement(tag);
    if (text !== undefined) element.textContent = text;
    if (className) element.className = className;
    return element;
  };
  const currentWork = agent => replayFrame ? replayFrame.states[agent.id] || {state:'unobserved',task:'Sem atividade nesta etapa'} : production && !codexEnabled ? {state:'planned',task:'IA não configurada. Nenhuma tarefa em execução.'} : activity[agent.id] || (!production && work[agent.id]) || {state:production ? 'available' : ['A01','A02'].includes(agent.id) ? 'available' : 'planned',task:'Nenhuma tarefa em execução'};
  const stateLabel = agent => {
    const state = currentWork(agent).state;
    if (replayFrame) {
      if (state === 'running') return 'Processando evento registrado no histórico';
      if (state === 'failed') return 'Falha registrada no histórico';
      return state === 'available' ? 'Concluído no histórico' : 'Sem atividade nesta etapa';
    }
    if (state === 'running') return agent.pose === 'seated' ? agent.seatKind === 'meeting' ? 'Em revisão coletiva' : 'Trabalho individual' : 'Em execução';
    if (state === 'review') return 'Parecer em revisão';
    if (state === 'queued' || state === 'unconfirmed') return currentWork(agent).task;
    if (state === 'failed') return 'Falha no processamento';
    const capability = production ? codexEnabled ? 'Nenhuma análise em execução' : 'IA não configurada' : ['A01','A02'].includes(agent.id) ? 'Disponível nesta prévia determinística' : 'Papel previsto';
    return capability + (agent.pose === 'seated' ? agent.seatKind === 'meeting' ? ' · À mesa do conselho' : ' · Na estação' : '');
  };
  const canDraw = () => ready && !document.hidden && inView;
  const canWalk = () => canDraw() && !replayFrame && !paused && !reduced.matches;
  const canAnimate = () => canWalk() && agents.some(agent => agent.path.length);
  const occupiedByOthers = agent => agents.filter(other => other !== agent).flatMap(other => other.target ? [{x:other.x,y:other.y},other.target] : [{x:other.x,y:other.y}]);
  const seatById = id => manifest?.world.seats?.find(seat => seat.id === id);
  const seatOwner = id => agents.find(agent => agent.seatId === id || agent.seatTarget === id);
  function standingPoint(agent) {
    const seat = seatById(agent.seatId);
    return seat ? model.nearestWalkable(grid,seat.approach,occupiedByOthers(agent)) : {x:agent.x,y:agent.y};
  }
  const desiredSeat = agent => currentWork(agent).seat || (!production && currentWork(agent).state === 'running' ? 'workstation' : null);
  const seatKind = seat => seat ? seat.kind || 'workstation' : null;
  function sitAgent(agent,seat) {
    if (!seat || replayFrame || desiredSeat(agent) !== seatKind(seat) || !agent.seated || agents.some(other => other !== agent && (other.seatId === seat.id || other.seatTarget === seat.id))) return false;
    agent.x = seat.position.x; agent.y = seat.position.y; agent.pose = 'seated'; agent.seatId = seat.id; agent.seatTarget = null;
    agent.seatAnchor = seat.anchor ? {...seat.anchor} : null;
    agent.seatPose = seat.pose || null; agent.seatKind = seat.kind || 'workstation';
    agent.seatMask = agent.seatKind === 'meeting' ? manifest.world.meetingTable?.polygon : null;
    agent.path = []; agent.target = null; agent.facing = seat.facing; agent.wait = 0;
    return true;
  }
  function stopRoute(agent) {
    agent.path = [];
    agent.target = null;
    agent.seatTarget = null;
    agent.roaming = false;
    agent.wait = 0;
  }

  function reconcileSeats() {
    if (!ready || replayFrame) return;
    for (const agent of agents) {
      const kind = desiredSeat(agent);
      if (kind && agent.roaming) stopRoute(agent);
      if (agent.seatTarget && seatKind(seatById(agent.seatTarget)) !== kind) stopRoute(agent);
      if (agent.seatId && agent.seatKind !== kind) {
        const exit = standingPoint(agent) || agent.home;
        Object.assign(agent,{x:exit.x,y:exit.y,pose:'standing',seatId:null,seatKind:null,seatPose:null});
        stopRoute(agent);
      }
    }
    const arrivals = agents.filter(agent => desiredSeat(agent) && !agent.seatId && !agent.seatTarget)
      .sort((a,b) => Number(currentWork(b).state === 'running')-Number(currentWork(a).state === 'running'));
    for (const agent of arrivals) {
      const kind = desiredSeat(agent);
      const free = manifest.world.seats.filter(seat => seatKind(seat) === kind && !seatOwner(seat.id));
      free.sort((a,b) => Math.hypot(a.approach.x-agent.x,a.approach.y-agent.y)-Math.hypot(b.approach.x-agent.x,b.approach.y-agent.y));
      for (const seat of free) {
        const approach = model.nearestWalkable(grid,seat.approach,occupiedByOthers(agent));
        if (!approach || Math.hypot(approach.x-seat.approach.x,approach.y-seat.approach.y)>grid.size*2) continue;
        if (!routeAgent(agent,approach,true)) continue;
        agent.seatTarget = seat.id;
        if (!agent.path.length) sitAgent(agent,seat);
        break;
      }
    }
  }

  function renderPortrait(target, agent, width = 64, height = 76) {
    if (!target) return;
    target.width = width * 2; target.height = height * 2;
    const portrait = target.getContext('2d');
    portrait.clearRect(0,0,target.width,target.height);
    portrait.imageSmoothingEnabled = false;
    const image = images.get(agent.atlas);
    if (!image) return;
    const frame = agent.frames[0], scale = Math.min(target.width/frame.w,target.height/frame.h);
    const w = Math.round(frame.w*scale), h = Math.round(frame.h*scale);
    portrait.drawImage(image,frame.x,frame.y,frame.w,frame.h,Math.round((target.width-w)/2),target.height-h,w,h);
  }

  function buildRoster() {
    const roster = $('agent-roster');
    if (!roster) return;
    rosterButtons.clear();
    const fragment = document.createDocumentFragment();
    for (const agent of agents) {
      const button = append('button',undefined,'agent-roster-item');
      button.type = 'button'; button.dataset.agent = agent.id;
      button.setAttribute('aria-controls','agent-panel'); button.setAttribute('aria-expanded','false');
      const portrait = append('canvas',undefined,'agent-roster-portrait');
      portrait.setAttribute('aria-hidden','true'); portrait.width = 96; portrait.height = 120;
      const copy = append('span',undefined,'agent-roster-copy');
      copy.append(append('strong',agent.name),append('span',agent.role),append('small',stateLabel(agent),'agent-roster-state'));
      button.append(portrait,copy);
      button.addEventListener('click',() => selectAgent(agent.id,button));
      rosterButtons.set(agent.id,button); fragment.append(button);
    }
    roster.replaceChildren(fragment);
    filterRoster();
  }

  function filterRoster() {
    const query = ($('agent-search')?.value || '').normalize('NFD').replace(/[\u0300-\u036f]/g,'').toLocaleLowerCase('pt-BR').trim();
    let shown = 0;
    for (const agent of agents) {
      const searchable = [agent.name,agent.role,agent.id].join(' ').normalize('NFD').replace(/[\u0300-\u036f]/g,'').toLocaleLowerCase('pt-BR');
      const button = rosterButtons.get(agent.id);
      if (button) { button.hidden = Boolean(query && !searchable.includes(query)); if (!button.hidden) shown++; }
    }
    const empty = $('agent-roster-empty');
    if (empty) empty.hidden = shown !== 0;
    if (query) announce(shown ? shown + (shown === 1 ? ' personagem encontrado.' : ' personagens encontrados.') : 'Nenhum personagem para esta busca.');
  }

  function updateWorkUI() {
    if ($('tour-office')) $('tour-office').disabled = !canWalk();
    for (const agent of agents) {
      const button = rosterButtons.get(agent.id), state = currentWork(agent).state;
      if (button) {
        button.classList.toggle('is-running',state === 'running');
        button.dataset.workState = state;
        const label = button.querySelector('.agent-roster-state');
        if (label) label.textContent = stateLabel(agent);
      }
    }
    if (selected) updatePanel();
    invalidate();
  }

  function updatePanel() {
    const agent = agents.find(item => item.id === selected);
    if (!agent) return;
    setText('agent-name',agent.name + (replayFrame ? ' · Histórico' : '')); setText('agent-role',agent.role); setText('agent-description',agent.description);
    setText('agent-state',stateLabel(agent)); setText('agent-current-task',currentWork(agent).task);
    setText('office-selection',agent.name + (replayFrame ? ' selecionado. Etapa do histórico em exibição.' : ' selecionado. ' + (desiredSeat(agent) ? currentWork(agent).task : 'Escolha um ponto livre do piso para caminhar.')));
    renderPortrait($('agent-avatar'),agent,88,108);
  }

  function selectAgent(id, source) {
    const agent = agents.find(item => item.id === id);
    if (!agent) return false;
    trigger = source || rosterButtons.get(id) || canvas;
    selected = id;
    for (const [agentId,button] of rosterButtons) {
      button.classList.toggle('is-selected',agentId === selected);
      button.setAttribute('aria-expanded',String(agentId === selected));
    }
    const panel = $('agent-panel');
    if (panel) { panel.hidden = false; updatePanel(); panel.focus({preventScroll:true}); }
    if (ready) focusPoint(agent,false);
    announce(agent.name + ', ' + agent.role + '. ' + stateLabel(agent) + '.');
    invalidate();
    window.dispatchEvent(new CustomEvent('office-agent-selected',{detail:{id:agent.id,name:agent.name}}));
    return true;
  }

  function setReplay(frame) {
    if (!frame || typeof frame !== 'object' || !frame.states || typeof frame.states !== 'object' || Array.isArray(frame.states) || !Number.isSafeInteger(frame.index) || frame.index < -1 || frame.index >= 200) return false;
    if (frame.index === -1 ? frame.sequence !== null || frame.created_at !== null || Object.keys(frame.states).length : !Number.isSafeInteger(frame.sequence) || frame.sequence <= 0 || typeof frame.created_at !== 'string' || !Number.isFinite(Date.parse(frame.created_at))) return false;
    const states = {}, tasks = {running:'Organização iniciada',available:'Organização concluída',failed:'Organização interrompida'};
    for (const [id,value] of Object.entries(frame.states)) {
      if (!ids.includes(id) || !value || !Object.hasOwn(tasks,value.state) || value.task !== tasks[value.state]) return false;
      states[id] = {state:value.state,task:value.task};
    }
    const entering = !replayFrame;
    replayFrame = {states,sequence:frame.sequence,created_at:frame.created_at,index:frame.index};
    if (entering) {
      for (const agent of agents) { stopRoute(agent); agent.retries = 0; }
      updateMotion();
    }
    updateWorkUI();
    if (entering) window.dispatchEvent(new CustomEvent('office-replay-mode',{detail:{active:true}}));
    return true;
  }

  function exitReplay() {
    if (!replayFrame) return false;
    replayFrame = null;
    reconcileSeats();
    updateWorkUI(); updateMotion();
    announce('Estado atual do escritório restaurado.');
    window.dispatchEvent(new CustomEvent('office-replay-mode',{detail:{active:false}}));
    return true;
  }

  function closePanel() {
    const panel = $('agent-panel'); if (panel) panel.hidden = true;
    selected = null;
    for (const button of rosterButtons.values()) { button.classList.remove('is-selected'); button.setAttribute('aria-expanded','false'); }
    setText('office-selection','Selecione um personagem no escritório ou na lista.');
    const returnTarget = trigger?.isConnected && trigger.getClientRects().length ? trigger : canvas;
    returnTarget.focus({preventScroll:true});
    invalidate();
    window.dispatchEvent(new CustomEvent('office-agent-selected',{detail:{id:null}}));
  }

  function resize() {
    const bounds = viewport.getBoundingClientRect();
    const next = {width:Math.max(1,bounds.width),height:Math.max(1,bounds.height)};
    const center = camera ? model.screenToWorld({x:size.width/2,y:size.height/2},camera) : null;
    const zoomLevel = camera && manifest ? camera.scale/model.fitCamera(manifest.world,size).scale : 1;
    size = next; dpr = Math.min(window.devicePixelRatio || 1,2);
    canvas.width = Math.round(size.width*dpr); canvas.height = Math.round(size.height*dpr);
    ctx.imageSmoothingEnabled = false;
    if (manifest) {
      const fit = model.fitCamera(manifest.world,size), scale = fit.scale*zoomLevel;
      camera = center ? model.clampCamera({scale,x:size.width/2-center.x*scale,y:size.height/2-center.y*scale},manifest.world,size) : fit;
    }
    updateZoom(); invalidate();
  }

  function updateZoom() {
    if (!camera || !manifest) return;
    const percent = Math.round(camera.scale/model.fitCamera(manifest.world,size).scale*100);
    setText('office-zoom',percent + '%');
    if ($('zoom-out')) $('zoom-out').disabled = !ready || percent <= 100;
    if ($('zoom-in')) $('zoom-in').disabled = !ready || percent >= 400;
  }

  function zoom(factor,anchor = {x:size.width/2,y:size.height/2}) {
    if (!ready) return;
    camera = model.zoomCamera(camera,factor,anchor,manifest.world,size);
    updateZoom(); invalidate();
  }

  function focusPoint(target, magnify = true) {
    if (!ready) return;
    const fit = model.fitCamera(manifest.world,size), scale = magnify ? Math.max(camera.scale,fit.scale*1.65) : camera.scale;
    camera = model.clampCamera({scale,x:size.width/2-target.x*scale,y:size.height/2-target.y*scale},manifest.world,size);
    updateZoom(); invalidate();
  }

  function routeAgent(agent,target,quiet = false) {
    if (replayFrame) return false;
    if (!ready || !agent || !model.isWalkable(grid,target)) {
      if (!quiet) announce('Escolha um ponto livre do piso, fora dos móveis.');
      return false;
    }
    const origin = standingPoint(agent);
    const route = origin ? model.findPath(grid,origin,target,occupiedByOthers(agent)) : [];
    if (!route.length) { if (!quiet) announce('O caminho está ocupado ou bloqueado. Escolha outro ponto do piso.'); return false; }
    agent.x = origin.x; agent.y = origin.y; agent.pose = 'standing'; agent.seatId = null; agent.seatKind = null; agent.seatPose = null; agent.seatTarget = null; agent.roaming = false;
    agent.path = route.slice(1); agent.target = {x:target.x,y:target.y}; agent.walkTime = 0; agent.wait = 0; agent.retries = 0;
    if (reduced.matches) {
      agent.x = target.x; agent.y = target.y; agent.path = []; agent.target = null;
      if (!quiet) announce(agent.name + ' está no destino. Movimento reduzido ativo.');
    } else if (!quiet) announce(agent.name + (paused ? ' caminhará ao retomar o ambiente.' : ' caminha pelo piso até o destino.'));
    invalidate();
    if (selected === agent.id) updatePanel();
    return true;
  }

  function walkSelectedTo(target) {
    const agent = agents.find(item => item.id === selected);
    return Boolean(agent && !desiredSeat(agent) && Number.isFinite(target?.x) && Number.isFinite(target?.y) && routeAgent(agent,target));
  }

  function tour() {
    if (!canWalk()) return false;
    const limit = viewport.clientWidth <= 640 ? 2 : 3;
    if (agents.filter(agent => agent.roaming).length >= limit) return false;
    for (let attempt = 0; attempt < agents.length; attempt++) {
      const agent = agents[routineIndex++ % agents.length];
      if (desiredSeat(agent) || agent.seatId || agent.path.length || agent.roamAfter > Date.now()) continue;
      const angle = Math.random()*Math.PI*2;
      const target = {x:agent.x+Math.cos(angle)*144,y:agent.y+Math.sin(angle)*144};
      const destination = model.nearestWalkable(grid,target,occupiedByOthers(agent));
      if (!destination || Math.hypot(destination.x-agent.x,destination.y-agent.y)<grid.size*2) continue;
      if (routeAgent(agent,destination,true)) {
        agent.roaming = true;
        return true;
      }
    }
    return false;
  }

  function scheduleTour(delay) {
    clearTimeout(routineTimer);
    if (canWalk()) routineTimer = setTimeout(() => {
      tour();
      scheduleTour(3000+Math.random()*3000);
    },delay);
  }

  function finishRoute(agent) {
    const seat = seatById(agent.seatTarget);
    if (agent.roaming) agent.roamAfter = Date.now()+8000+Math.random()*12000;
    stopRoute(agent);
    if (seat) sitAgent(agent,seat);
  }

  function advance(delta) {
    for (const agent of agents) {
      if (!agent.path.length) continue;
      const next = agent.path[0], dx = next.x-agent.x, dy = next.y-agent.y, length = Math.hypot(dx,dy), step = Math.min(length,delta*100);
      const candidate = length < 0.001 ? next : {x:agent.x+dx/length*step,y:agent.y+dy/length*step};
      if (agents.some(other => other !== agent && Math.hypot(other.x-candidate.x,other.y-candidate.y) < 22)) {
        agent.wait += delta;
        if (agent.wait > 0.8) {
          const route = model.findPath(grid,{x:agent.x,y:agent.y},agent.target,occupiedByOthers(agent));
          agent.wait = 0; agent.retries++;
          if (route.length && agent.retries < 4) agent.path = route.slice(1);
          else {
            if (agent.roaming) agent.roamAfter = Date.now()+8000;
            stopRoute(agent);
          }
        }
        continue;
      }
      if (Math.abs(dx) > 0.1) agent.facing = dx < 0 ? -1 : 1;
      agent.x = candidate.x; agent.y = candidate.y; agent.walkTime += delta*1000; agent.wait = 0;
      if (length <= step+0.001) agent.path.shift();
      if (!agent.path.length) {
        const arrival = agent.seatTarget;
        finishRoute(agent);
        if (arrival) updateWorkUI();
      }
    }
  }

  function draw() {
    if (!canDraw() || !camera) return;
    frameCount++;
    ctx.setTransform(dpr,0,0,dpr,0,0); ctx.imageSmoothingEnabled = false;
    ctx.fillStyle = '#12242a'; ctx.fillRect(0,0,size.width,size.height);
    ctx.translate(camera.x,camera.y); ctx.scale(camera.scale,camera.scale);
    const background = images.get('background');
    if (background) ctx.drawImage(background,0,0,manifest.world.width,manifest.world.height);
    if (display && manifest.world.financeScreen) display.draw(ctx,manifest.world.financeScreen,finance,financeConnection);
    for (const agent of [...agents].sort((a,b) => a.y-b.y || a.id.localeCompare(b.id))) {
      const walking = Boolean(agent.path.length && !replayFrame && !paused && !reduced.matches && !agent.wait);
      const frame = model.animationFrame(agent,agent.walkTime,walking), rect = model.spriteBounds(agent,frame.rect);
      if (agent.id === selected) {
        ctx.beginPath(); ctx.ellipse(agent.x,agent.y-3,23,9,0,0,Math.PI*2);
        ctx.fillStyle = '#f3d39275'; ctx.fill(); ctx.lineWidth = 2; ctx.strokeStyle = '#fff0bc'; ctx.stroke();
      }
      const atlas = images.get(frame.atlas);
      if (atlas) {
        const meeting = agent.pose === 'seated' && agent.seatKind === 'meeting' && manifest.world.meetingTable;
        const paint = fraction => {
          ctx.save(); ctx.translate(rect.x+(agent.facing === -1 ? rect.width : 0),rect.y); ctx.scale(agent.facing,1);
          ctx.drawImage(atlas,frame.rect.x,frame.rect.y,frame.rect.w,frame.rect.h*fraction,0,0,rect.width,rect.height*fraction);
          ctx.restore();
        };
        if (meeting) {
          // Lower bodies stay behind the real table; arms and torsos remain above it.
          ctx.save(); ctx.beginPath(); ctx.rect(0,0,manifest.world.width,manifest.world.height);
          meeting.polygon.forEach(([x,y],index) => index ? ctx.lineTo(x,y) : ctx.moveTo(x,y)); ctx.closePath(); ctx.clip('evenodd');
          paint(1); ctx.restore();
          paint(model.seatedSprite(agent).upperBody);
        } else paint(1);
      }
      // Restore each chair back before drawing colleagues standing further forward.
      const seat = seatById(agent.seatId);
      if (background && seat) for (const shape of seat.occluders) {
        ctx.save(); ctx.beginPath(); shape.forEach(([x,y],index) => index ? ctx.lineTo(x,y) : ctx.moveTo(x,y)); ctx.closePath(); ctx.clip();
        ctx.drawImage(background,0,0,manifest.world.width,manifest.world.height); ctx.restore();
      }
      if (currentWork(agent).state === 'running') {
        ctx.fillStyle = '#79dbc0'; ctx.fillRect(Math.round(agent.x-3),Math.round(rect.y-12),6,6);
      }
    }
    ctx.setTransform(dpr,0,0,dpr,0,0);
    window.dispatchEvent(new CustomEvent('office-frame'));
    updateCursor();
  }

  function tick(time) {
    raf = 0;
    if (!canDraw()) { previousTime = 0; return; }
    const delta = previousTime ? Math.min((time-previousTime)/1000,0.05) : 0;
    previousTime = time;
    if (canAnimate()) advance(delta);
    draw();
    if (canAnimate()) raf = requestAnimationFrame(tick);
    else previousTime = 0;
  }

  function invalidate() {
    updateCursor();
    if (!canDraw() || raf) return;
    if (paused || reduced.matches) { draw(); return; }
    raf = requestAnimationFrame(tick);
  }

  function updateMotion() {
    if (raf) cancelAnimationFrame(raf);
    raf = 0; previousTime = 0;
    viewport.dataset.paused = String(Boolean(replayFrame) || paused || reduced.matches || document.hidden || !inView);
    viewport.dataset.inView = String(inView);
    document.body.dataset.ambientPaused = String(Boolean(replayFrame) || paused || reduced.matches);
    const button = $('pause-office');
    if (button) {
      button.setAttribute('aria-pressed',String(paused));
      button.setAttribute('aria-label',paused ? 'Retomar ambiente' : 'Pausar ambiente');
      button.title = paused ? 'Retomar ambiente' : 'Pausar ambiente';
    }
    if (reduced.matches) {
      for (const agent of agents) if (agent.target) {
        if (!agent.roaming) { agent.x = agent.target.x; agent.y = agent.target.y; }
        finishRoute(agent);
      }
    }
    if ($('tour-office')) $('tour-office').disabled = !canWalk();
    scheduleTour(600);
    invalidate();
  }

  async function loadImage(url) {
    return new Promise((resolve,reject) => {
      const image = new Image();
      image.onload = () => resolve(image);
      image.onerror = () => reject(new Error('Não foi possível carregar uma imagem do escritório.'));
      image.src = url;
    });
  }

  async function load() {
    const attempt = ++generation;
    ready = false; errorText = ''; images.clear(); updateMotion();
    viewport.dataset.officeReady = 'false';
    if ($('office-loading')) $('office-loading').hidden = false;
    if ($('office-error')) $('office-error').hidden = true;
    announce('Carregando o cenário e os 23 personagens…');
    try {
      const response = await fetch('/static/office/manifest.json',{credentials:'same-origin',cache:'no-cache'});
      if (!response.ok) throw new Error('O mapa do escritório não pôde ser carregado.');
      const source = model.validateManifest(await response.json());
      if (attempt !== generation) return;
      manifest = source; grid = model.createGrid(manifest.world,24);
      const occupied = [];
      agents = manifest.agents.map(agent => {
        const home = model.nearestWalkable(grid,agent.home,occupied);
        if (!home) throw new Error('O piso não tem espaço livre para os 23 personagens.');
        occupied.push(home);
        return {...agent,home,x:home.x,y:home.y,path:[],target:null,walkTime:0,facing:1,wait:0,retries:0,pose:'standing',seatId:null,seatTarget:null,homeSeat:null};
      });
      selected = null; if ($('agent-panel')) $('agent-panel').hidden = true;
      buildRoster(); updateWorkUI(); camera = null; resize();
      const loaded = await Promise.all([loadImage(manifest.world.background),...manifest.atlases.map(atlas => loadImage(atlas.url))]);
      if (attempt !== generation) return;
      if (loaded[0].naturalWidth !== manifest.world.width || loaded[0].naturalHeight !== manifest.world.height) throw new Error('As dimensões do cenário não correspondem ao mapa.');
      images.set('background',loaded[0]);
      manifest.atlases.forEach((atlas,index) => {
        const image = loaded[index+1];
        if (image.naturalWidth !== atlas.width || image.naturalHeight !== atlas.height) throw new Error('As dimensões de um atlas de personagens não correspondem ao mapa.');
        images.set(atlas.id,image);
      });
      ready = true;
      if (lastSnapshot) work = model.reconcileWorkState(work,lastSnapshot);
      reconcileSeats();
      viewport.dataset.officeReady = 'true';
      if ($('office-loading')) $('office-loading').hidden = true;
      for (const agent of agents) renderPortrait(rosterButtons.get(agent.id)?.querySelector('canvas'),agent,48,60);
      updateWorkUI(); updateZoom(); updateMotion();
      announce('Escritório pronto. 23 personagens disponíveis para explorar.');
      window.dispatchEvent(new CustomEvent('office-ready',{detail:{count:agents.length,agents:agents.map(({id,name}) => ({id,name}))}}));
    } catch (error) {
      if (attempt !== generation) return;
      errorText = error instanceof Error ? error.message : 'O escritório não pôde ser carregado.';
      ready = false; viewport.dataset.officeReady = 'false';
      if ($('office-loading')) $('office-loading').hidden = true;
      const errorPanel = $('office-error');
      if (errorPanel) {
        errorPanel.hidden = false;
        const message = errorPanel.querySelector('[data-office-error-message]');
        if (message) message.textContent = errorText + ' Tente carregar novamente. A lista de personagens continua disponível.';
      }
      announce(errorText + ' Use Tentar novamente para carregar a cena.');
      updateWorkUI();
    }
  }

  function localPoint(event) {
    const bounds = canvas.getBoundingClientRect();
    return {x:event.clientX-bounds.left,y:event.clientY-bounds.top};
  }

  function targetAt(p) {
    const objects = (manifest.world.interactives || []).filter(object => object.action.type === 'agent' || window.ConselhoWorkspace?.openPanel);
    return model.hitTarget(agents,objects,model.screenToWorld(p,camera));
  }

  function updateCursor() {
    let cursor = 'grab', title = '';
    if (drag?.moved || pinch) cursor = 'grabbing';
    else if (ready && camera && hover) {
      const p = localPoint(hover), bounds = canvas.getBoundingClientRect();
      if (p.x >= 0 && p.y >= 0 && p.x < bounds.width && p.y < bounds.height) {
        const target = targetAt(p);
        if (target) {
          cursor = 'pointer';
          title = target.kind === 'agent' ? 'Conhecer ' + target.agent.name : target.object.label;
        }
      }
    }
    if (canvas.dataset.cursor !== cursor) canvas.dataset.cursor = cursor;
    if (canvas.title !== title) canvas.title = title;
  }

  function trackHover(event) {
    hover = event.pointerType === 'mouse' || event.pointerType === 'pen' ? {clientX:event.clientX,clientY:event.clientY} : null;
  }

  canvas.addEventListener('pointerdown',event => {
    if (!ready || (event.pointerType === 'mouse' && event.button !== 0)) return;
    trackHover(event);
    const p = localPoint(event); pointers.set(event.pointerId,p);
    canvas.setPointerCapture(event.pointerId);
    if (pointers.size === 1) drag = {start:p,last:p,moved:false};
    else if (pointers.size === 2) {
      const [a,b] = [...pointers.values()];
      pinch = {distance:Math.hypot(a.x-b.x,a.y-b.y),center:{x:(a.x+b.x)/2,y:(a.y+b.y)/2}};
      if (drag) drag.moved = true;
    }
    updateCursor();
  });
  canvas.addEventListener('pointermove',event => {
    trackHover(event);
    if (!pointers.has(event.pointerId) || !ready) { updateCursor(); return; }
    const p = localPoint(event); pointers.set(event.pointerId,p);
    if (pointers.size >= 2 && pinch) {
      const [a,b] = [...pointers.values()], distance = Math.hypot(a.x-b.x,a.y-b.y), center = {x:(a.x+b.x)/2,y:(a.y+b.y)/2};
      zoom(pinch.distance ? distance/pinch.distance : 1,center);
      camera = model.clampCamera({...camera,x:camera.x+center.x-pinch.center.x,y:camera.y+center.y-pinch.center.y},manifest.world,size);
      pinch = {distance,center}; invalidate();
    } else if (drag) {
      if (Math.hypot(p.x-drag.start.x,p.y-drag.start.y) > 6) drag.moved = true;
      if (drag.moved) {
        camera = model.clampCamera({...camera,x:camera.x+p.x-drag.last.x,y:camera.y+p.y-drag.last.y},manifest.world,size);
        invalidate();
      }
      drag.last = p;
    }
    updateCursor();
  });
  function endPointer(event,canceled = false) {
    if (!pointers.has(event.pointerId)) return;
    const click = !canceled && pointers.size === 1 && drag && !drag.moved, p = localPoint(event);
    if (canceled) hover = null; else trackHover(event);
    pointers.delete(event.pointerId); pinch = null;
    if (canvas.hasPointerCapture(event.pointerId)) canvas.releasePointerCapture(event.pointerId);
    if (click && ready) {
      const target = targetAt(p), worldPoint = model.screenToWorld(p,camera);
      if (target?.kind === 'agent') selectAgent(target.agent.id,canvas);
      else if (target?.kind === 'object') {
        const action = target.object.action;
        if (action.type === 'agent') selectAgent(action.target,canvas);
        else {
          if (viewport.dataset.expanded === 'true') setExpanded(false);
          window.ConselhoWorkspace.openPanel(action.target,canvas);
        }
      }
      else if (replayFrame) announce('Etapa do histórico em exibição. Selecione um personagem para consultar seu estado.');
      else if (selected) walkSelectedTo(worldPoint);
      else announce('Selecione um personagem para levá-lo a um ponto livre do piso.');
    }
    drag = pointers.size ? {start:[...pointers.values()][0],last:[...pointers.values()][0],moved:true} : null;
    updateCursor();
  }
  canvas.addEventListener('pointerup',event => endPointer(event));
  canvas.addEventListener('pointercancel',event => endPointer(event,true));
  canvas.addEventListener('lostpointercapture',event => endPointer(event,true));
  canvas.addEventListener('pointerleave',() => { hover = null; updateCursor(); });
  canvas.addEventListener('wheel',event => {
    if (!ready || !(event.ctrlKey || event.metaKey || document.activeElement === canvas)) return;
    event.preventDefault(); zoom(Math.exp(-event.deltaY*0.0015),localPoint(event));
  },{passive:false});
  canvas.addEventListener('keydown',event => {
    if (!ready) return;
    if (event.key === '+' || event.key === '=') { event.preventDefault(); zoom(1.25); }
    else if (event.key === '-') { event.preventDefault(); zoom(0.8); }
    else if (event.key === '0') { event.preventDefault(); camera = model.fitCamera(manifest.world,size); updateZoom(); invalidate(); }
    else if (['ArrowLeft','ArrowRight','ArrowUp','ArrowDown'].includes(event.key)) {
      event.preventDefault();
      const x = event.key === 'ArrowLeft' ? 60 : event.key === 'ArrowRight' ? -60 : 0;
      const y = event.key === 'ArrowUp' ? 60 : event.key === 'ArrowDown' ? -60 : 0;
      camera = model.clampCamera({...camera,x:camera.x+x,y:camera.y+y},manifest.world,size); invalidate();
    }
  });

  $('zoom-in')?.addEventListener('click',() => zoom(1.25));
  $('zoom-out')?.addEventListener('click',() => zoom(0.8));
  $('zoom-reset')?.addEventListener('click',() => { if (!ready) return; camera = model.fitCamera(manifest.world,size); updateZoom(); invalidate(); });
  $('pause-office')?.addEventListener('click',() => {
    paused = !paused; try { localStorage.setItem('conselho-ambient-paused',String(paused)); } catch {}
    updateMotion(); announce(paused ? 'Ambiente pausado. A seleção e os controles continuam disponíveis.' : 'Movimento do ambiente retomado.');
  });
  function setExpanded(expanded) {
    viewport.dataset.expanded = String(expanded);
    const surface = viewport.closest('.office-surface'); if (surface) surface.classList.toggle('is-expanded',expanded);
    const button = $('expand-office'); button?.setAttribute('aria-pressed',String(expanded));
    button?.setAttribute('aria-label',expanded ? 'Recolher escritório' : 'Ampliar escritório');
    requestAnimationFrame(resize);
  }
  $('expand-office')?.addEventListener('click',() => setExpanded(viewport.dataset.expanded !== 'true'));
  document.querySelectorAll('[data-room-focus]').forEach(button => button.addEventListener('click',() => {
    const room = button.dataset.roomFocus;
    if (!ready || !manifest.world.rooms[room]) return;
    document.querySelectorAll('[data-room-focus]').forEach(item => item.setAttribute('aria-pressed',String(item === button)));
    focusPoint(room === 'council' ? manifest.world.meetingTable?.focus || manifest.world.rooms[room] : manifest.world.rooms[room]); announce('Área de ' + roomNames[room] + ' em foco.');
  }));
  $('agent-search')?.addEventListener('input',filterRoster);
  $('agent-close')?.addEventListener('click',closePanel);
  $('tour-office')?.addEventListener('click',() => {
    if (tour()) announce('Um personagem está circulando pelo escritório.');
  });
  $('office-retry')?.addEventListener('click',load);
  document.addEventListener('keydown',event => {
    if (document.body.dataset.drawerOpen) return;
    if (event.key === 'Escape' && selected) { event.preventDefault(); closePanel(); }
  });
  window.addEventListener('workspace-panel-change',event => {
    if (event.detail?.panel === 'finance' && viewport.dataset.expanded === 'true') setExpanded(false);
  });
  window.addEventListener('finance-summary',event => {
    const next = display?.project(event.detail);
    if (next) { finance = next; invalidate(); }
  });
  window.addEventListener('finance-connection',event => {
    financeConnection = event.detail?.state || 'stale'; invalidate();
  });
  window.addEventListener('office-event',event => {
    if (production) return;
    const prior = work;
    work = model.reduceWorkState(work,event.detail,ids);
    if (work !== prior) { reconcileSeats(); updateWorkUI(); }
  });
  window.addEventListener('office-activity',event => {
    const next = event.detail || {};
    const changed = JSON.stringify(next) !== JSON.stringify(activity);
    activity = next;
    reconcileSeats();
    if (changed) updateWorkUI();
  });
  window.addEventListener('conselho-state',event => {
    const snapshot = event.detail?.snapshot || event.detail;
    if (!snapshot || typeof snapshot.pending !== 'number') return;
    lastSnapshot = {pending:snapshot.pending}; work = model.reconcileWorkState(work,lastSnapshot); reconcileSeats(); updateWorkUI();
  });
  document.addEventListener('visibilitychange',() => {
    if (!document.hidden && lastSnapshot?.pending === 0) { work = model.reconcileWorkState(work,lastSnapshot); reconcileSeats(); updateWorkUI(); }
    updateMotion();
  });
  window.addEventListener('pagehide',() => { clearTimeout(routineTimer); if (raf) cancelAnimationFrame(raf); raf = 0; previousTime = 0; });
  window.addEventListener('pageshow',() => updateMotion());
  reduced.addEventListener('change',updateMotion);
  if ('IntersectionObserver' in window) new IntersectionObserver(entries => { inView = entries.some(entry => entry.isIntersecting); updateMotion(); },{threshold:0.01}).observe(viewport);
  if ('ResizeObserver' in window) new ResizeObserver(resize).observe(viewport);
  else window.addEventListener('resize',resize);

  window.ConselhoOffice = Object.freeze({
    anchorFor:id => {
      if (!ready || !camera) return null;
      const agent=agents.find(item=>item.id===id);if(!agent)return null;
      const frame=model.animationFrame(agent,agent.walkTime,Boolean(agent.path.length && canWalk() && !agent.wait)), rect=model.spriteBounds(agent,frame.rect);
      return model.worldToScreen({x:agent.x,y:rect.y},camera);
    },
    focusAgent:id => {const agent=agents.find(item=>item.id===id);if(agent && ready){focusPoint(agent);return true;}return false;},
    selectAgent:id => selectAgent(id),
    walkSelectedTo,
    setReplay,
    exitReplay,
    getState:() => {
      const snapshot = {
        ready,error:errorText,count:agents.length,selected,paused,reducedMotion:reduced.matches,inView,hidden:document.hidden,
        replay:replayFrame ? {active:true,sequence:replayFrame.sequence,index:replayFrame.index} : null,
        finance:finance ? {...finance,connection:financeConnection} : null,
        renderLoopActive:Boolean(raf),frameCount,camera:camera ? {...camera} : null,
        agents:agents.map(agent => ({id:agent.id,name:agent.name,x:agent.x,y:agent.y,home:{...agent.home},target:agent.target ? {...agent.target} : null,
          path:agent.path.map(p => ({...p})),frame:model.animationFrame(agent,agent.walkTime,Boolean(agent.path.length && !replayFrame && !paused && !reduced.matches && !agent.wait)).index,
          pose:agent.pose,roaming:Boolean(agent.roaming),seatId:agent.seatId,seatTarget:agent.seatTarget,seatKind:agent.seatKind || null,seatPose:agent.seatPose || null,
          facing:agent.facing,state:currentWork(agent).state,task:currentWork(agent).task}))
      };
      const freeze = value => { if (value && typeof value === 'object') { Object.freeze(value); Object.values(value).forEach(freeze); } return value; };
      return freeze(snapshot);
    }
  });
  document.fonts?.ready.then(invalidate);
  resize(); load();
})();
