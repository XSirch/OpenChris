(() => {
  'use strict';
  const $ = id => document.getElementById(id), model = window.ConselhoDialogueModel;
  const layer = $('office-dialogue-layer'), bar = $('office-dialogue');
  if (!layer || !bar || !model) return;

  const reduced = matchMedia('(prefers-reduced-motion: reduce)');
  const node = (tag, text, className) => {
    const element = document.createElement(tag);
    if (text) element.textContent = text;
    if (className) element.className = className;
    return element;
  };
  const dock = node('div', '', 'office-speech-dock');
  layer.append(dock);
  const replayId = new URLSearchParams(location.search).get('replay');
  const explicit = /^[a-f0-9-]{36}$/i.test(replayId || '') ? replayId : null;
  const dateFormat = new Intl.DateTimeFormat('pt-BR', {
    timeZone:'America/Sao_Paulo', day:'2-digit', month:'2-digit', hour:'2-digit', minute:'2-digit'
  });
  const stamp = value => Number.isFinite(Date.parse(value)) ? dateFormat.format(new Date(value)) : 'data não registrada';
  const focusAgent = id => window.ConselhoOffice?.focusAgent(id);
  let names = {}, status, jobs = [], autoUntil = 0, manualUntil = 0, activities = {};
  let selected, replay = false, archive = Boolean(explicit), playing = false, index = 0;
  let data = null, wanted = '', generation = 0, bubbles = [], timer, expiry, signature = '';

  function position() {
    const viewport = {width:layer.clientWidth, height:layer.clientHeight};
    if (layer.hidden || !viewport.width) return;
    const placed = [];
    const panel = $('agent-panel');
    if (panel && !panel.hidden) {
      const surface = layer.getBoundingClientRect(), rect = panel.getBoundingClientRect();
      placed.push({left:rect.left-surface.left,top:rect.top-surface.top,width:rect.width,height:rect.height});
    }
    for (const {element,message} of bubbles) {
      const anchor = window.ConselhoOffice?.anchorFor?.(message.agentId);
      // Measure the same width whether the bubble is anchored or in the fallback strip.
      const width = element.offsetWidth, height = element.offsetHeight;
      const mobile = viewport.width <= 640;
      const box = !mobile || element.dataset.primary === 'true' ? model.place(anchor,viewport,width,height) : null;
      const fits = box && !placed.some(rect => box.left < rect.left+rect.width+8 && box.left+width > rect.left-8 &&
        box.top < rect.top+rect.height+8 && box.top+height > rect.top-8);
      const parent = fits ? layer : dock;
      if (element.parentNode !== parent) {
        const focused = element.contains(document.activeElement) && document.activeElement;
        parent.append(element);
        if (focused) focused.focus({preventScroll:true});
      }
      element.dataset.docked = String(!fits);
      if (!fits) continue;
      element.style.left = box.left+'px';
      element.style.top = box.top+'px';
      element.style.setProperty('--bubble-tail',box.tail+'px');
      element.dataset.above = String(box.above);
      placed.push({...box,width,height});
    }
  }

  function show(messages) {
    const next = JSON.stringify(messages);
    if (next === signature) return;
    signature = next;
    const focused = bubbles.find(({element}) => element.contains(document.activeElement));
    for (const {element} of bubbles) element.remove();
    bubbles = messages.map((message,index) => {
      const el = node('article', '', 'office-speech');
      el.dataset.agent = message.agentId;
      el.dataset.activity = message.state;
      el.dataset.primary = String(index===0);
      el.append(node('strong',names[message.agentId] || message.agentId),node('span',message.text,'speech-excerpt'));
      if (message.href) {
        const link = node('a',message.state === 'running' || message.state === 'queued' ? 'Ver etapa' : 'Ler parecer','speech-read');
        link.href = message.href;
        el.append(link);
      }
      const frame = node('button','Enquadrar','speech-focus');
      frame.type = 'button';
      frame.setAttribute('aria-label','Enquadrar '+(names[message.agentId] || message.agentId));
      frame.addEventListener('click',() => focusAgent(message.agentId));
      el.append(frame);
      layer.append(el);
      return {element:el,message};
    });
    position();
    if (focused) {
      const replacement = bubbles.find(({message}) => message.agentId === focused.message.agentId);
      (replacement?.element.querySelector('button') || $('dialogue-focus')).focus({preventScroll:true});
    }
  }

  function schedule() {
    clearTimeout(timer);
    if (archive && playing && !reduced.matches && !replay && !document.hidden && data?.messages.length > 1) {
      timer = setTimeout(() => step(1),9000);
    }
  }

  function render() {
    const active = Object.entries(activities).filter(([,item]) => item.seat);
    const visible = active.map(([agentId,item]) => ({agentId,state:item.state,text:item.task,href:item.href}));
    if (selected && !visible.some(message => message.agentId === selected)) {
      const item = activities[selected] || {state:'available',task:'Sem tarefa em execução'};
      visible.unshift({agentId:selected,state:item.state,text:item.task,href:item.href});
    }
    if (selected) visible.sort((a,b) => Number(b.agentId === selected)-Number(a.agentId === selected));
    const historical = archive ? data?.messages[index] : null;
    if (historical && !visible.some(message => message.agentId === historical.agentId)) {
      visible.push({...historical,state:'historical',text:'Histórico · '+historical.text});
    }
    show(visible);
    const summary = active.find(([,item]) => item.state === 'running');
    const label = historical ? 'Parecer gravado em '+stamp(historical.at)+' · '+(index+1)+' de '+data.messages.length :
      summary ? (names[summary[0]] || summary[0])+' · '+summary[1].task :
      status?.paused ? 'Acompanhamento pausado' : status?.state === 'waiting_budget' || Object.values(activities).some(item => item.task === 'Aguardando cota') ?
      'Aguardando a cota diária' : Object.values(activities).some(item => item.state === 'unconfirmed') ?
      'Execução sem confirmação' : 'Nenhuma análise em execução no escritório.';
    if ($('office-dialogue-label').textContent !== label) $('office-dialogue-label').textContent = label;
    $('dialogue-report').href = data ? '/reports?run='+data.runId : '/reports';
    $('dialogue-prev').disabled = !data?.messages.length;
    $('dialogue-next').disabled = !data?.messages.length;
    $('dialogue-pause').disabled = !data?.messages.length;
    $('dialogue-pause').textContent = archive ? 'Voltar ao vivo' : 'Ver pareceres';
    $('dialogue-pause').setAttribute('aria-pressed',String(archive));
    $('dialogue-focus').disabled = !visible.length;
    schedule();
  }

  function step(delta) {
    if (!data?.messages.length) return;
    index = archive ? (index+delta+data.messages.length)%data.messages.length : delta < 0 ? data.messages.length-1 : 0;
    archive = true;
    render();
  }

  async function load(run, key) {
    if (key === wanted) return;
    wanted = key;
    const token = ++generation, controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(),12000);
    try {
      const response = await fetch('/api/autonomy/runs/'+encodeURIComponent(run.id),{credentials:'same-origin',signal:controller.signal});
      if (!response.ok) throw new Error('Parecer indisponível');
      const detail = await response.json();
      if (token !== generation) return;
      const projected = model.project(detail,names);
      if (data?.runId !== projected?.runId) index = 0;
      data = projected;
      index = Math.min(index,Math.max(0,(data?.messages.length || 0)-1));
      render();
    } catch {
      if (token === generation) wanted = '';
    } finally { clearTimeout(timeout); }
  }

  function update() {
    const now = Date.now();
    const automatic = now < autoUntil ? status : status && {...status,heartbeat_at:null};
    const manual = now < manualUntil ? jobs : jobs.map(job => ({...job,lease_expires_at:null}));
    activities = model.activities(automatic,manual,now);
    window.dispatchEvent(new CustomEvent('office-activity',{detail:activities}));
    render();
    clearTimeout(expiry);
    const deadlines = [autoUntil, manualUntil, model.heartbeatDeadline(automatic,now),
      ...(automatic?.tasks || []).filter(task => task.status === 'running').map(task => Date.parse(task.lease_expires_at)),
      ...manual.filter(job => job.status === 'running').map(job => Date.parse(job.lease_expires_at))];
    const next = Math.min(...deadlines.filter(time => time > now));
    if (Number.isFinite(next)) expiry = setTimeout(update,next-now+1);
  }

  window.addEventListener('autonomy-status',event => {
    status = event.detail;
    autoUntil = Date.now()+65000;
    update();
    const running = model.runningTasks(status)[0];
    const run = explicit ? {id:explicit} : status.runs?.find(item => item.id === running?.run_id) || status.runs?.[0];
    if (run) load(run,JSON.stringify([run.id,run.status,(status.tasks || []).filter(task => task.run_id === run.id && task.status === 'complete').map(task => task.id)]));
  });
  window.addEventListener('conselho-state',event => {
    jobs = event.detail.active_jobs || [];
    manualUntil = Date.now()+65000;
    update();
  });
  window.addEventListener('conselho-agents',event => {
    const next = Object.fromEntries(event.detail.map(agent => [agent.id,agent.name]));
    if (JSON.stringify(names) !== JSON.stringify(next)) { names = next; signature = ''; render(); }
  });
  window.addEventListener('conselho-state-unavailable',() => { manualUntil = 0; update(); });
  window.addEventListener('autonomy-status-unavailable',() => { autoUntil = 0; update(); });
  window.addEventListener('office-agent-selected',event => { selected = event.detail.id; render(); });
  window.addEventListener('office-frame',position);
  window.addEventListener('office-ready',event => {
    const roster = event.detail.agents || [];
    names = {...names,...Object.fromEntries(roster.map(agent => [agent.id,agent.name]))};
    signature = '';
    render();
  });
  window.addEventListener('office-replay-mode',event => {
    replay = event.detail.active;
    layer.hidden = replay;
    render();
    if (!replay) position();
  });
  $('dialogue-prev').addEventListener('click',() => { playing = false; step(-1); });
  $('dialogue-next').addEventListener('click',() => { playing = false; step(1); });
  $('dialogue-pause').addEventListener('click',() => { archive = !archive; playing = archive; render(); });
  $('dialogue-focus').addEventListener('click',() => { if (bubbles.length) focusAgent(bubbles[0].message.agentId); });
  layer.addEventListener('focusin',() => { playing = false; schedule(); });
  layer.addEventListener('pointerenter',() => { playing = false; schedule(); });
  reduced.addEventListener('change',schedule);
  document.addEventListener('visibilitychange',() => { if (!document.hidden) update(); else schedule(); });
  window.addEventListener('pagehide',() => { generation++; wanted = ''; clearTimeout(timer); clearTimeout(expiry); });
  window.addEventListener('pageshow',event => { if (event.persisted) update(); });
  if (explicit) load({id:explicit},'explicit');
  window.ConselhoDialogue = Object.freeze({getState:() => ({
    runId:data?.runId || null, historical:archive, messages:data?.messages.length || 0, index, paused:!playing,
    active:Object.keys(activities).find(id => activities[id].state === 'running') || null
  })});
})();
