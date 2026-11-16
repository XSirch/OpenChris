(() => {
  'use strict';
  const $ = id => document.getElementById(id), panel = $('activity-panel'), model = window.ConselhoReplayModel;
  if (!panel || !model) return;
  const names = new Map([...$('activity-agent').options].filter(o=>o.value).map(o=>[o.value,o.textContent]));
  const node = (tag,text,cls) => { const el=document.createElement(tag); if(text!==undefined)el.textContent=text; if(cls)el.className=cls; return el; };
  const stamp = value => value ? new Intl.DateTimeFormat('pt-BR',{timeZone:'America/Sao_Paulo',day:'2-digit',month:'2-digit',hour:'2-digit',minute:'2-digit',second:'2-digit'}).format(new Date(value)) : 'Não registrado';
  const channel = value => value === 'telegram' ? 'Telegram simulado' : 'Dashboard';
  const status = value => ({complete:'Concluída',failed:'Falhou',pending:'Na fila',incomplete:'Sem encerramento registrado'})[value] || 'Registro parcial';
  let generation=0, controller, nextBefore=null, recording=null, position=-1, playing=false, timer=0, historical=false, retry=null;
  let knownRuns=new Set(), participant=null;

  async function request(path,signal) {
    const response=await fetch(path,{credentials:'same-origin',signal});
    const data=await response.json();
    if(!response.ok){ if(response.status===401)location.assign('/login'); throw new Error(data.error || 'Não foi possível consultar as atividades.'); }
    return data;
  }
  function begin() {
    controller?.abort(); controller=new AbortController(); generation++;
    $('activity-error').hidden=true; $('activity-loading').hidden=false;
    return {generation,signal:controller.signal};
  }
  function fail(error,attempt) {
    if(error.name==='AbortError'||attempt!==generation)return;
    $('activity-error-text').textContent=error.message; $('activity-error').hidden=false;
    $('activity-error').focus({preventScroll:true});
  }
  function pause() { clearTimeout(timer); timer=0; playing=false; updateControls(); }
  function exit() {
    pause(); historical=false; position=-1;
    window.ConselhoOffice?.exitReplay();
    $('office-replay-banner').hidden=true; delete document.body.dataset.replaying;
    updateControls();
  }
  function updateControls() {
    const count=recording?.events.length || 0;
    $('activity-play').disabled=count===0; $('activity-prev').disabled=count===0||position<0;
    $('activity-next').disabled=count===0||position>=count-1;
    $('activity-play').textContent=playing?'Pausar':position===count-1&&count?'Rever':'Reproduzir';
    $('activity-play').setAttribute('aria-pressed',String(playing));
    $('activity-position').disabled=count===0; $('activity-position').max=Math.max(-1,count-1);
    $('activity-position').value=position;
    const label=position<0?'Início':('Etapa '+(position+1)+' de '+count);
    $('activity-position').setAttribute('aria-valuetext',label);
    $('activity-step-label').textContent=historical?label:'Pronto para rever';
    document.querySelectorAll('#activity-events li').forEach((el,index)=>{
      el.classList.toggle('is-current',historical&&index===position);el.classList.toggle('is-past',historical&&index<position);
      const button=el.querySelector('button');
      if(historical&&index===position)button.setAttribute('aria-current','step');else button.removeAttribute('aria-current');
    });
  }
  function showPosition(index) {
    if(!recording)return;
    position=Math.max(-1,Math.min(index,recording.events.length-1));
    const frame=model.stateAt(recording.events,position);
    window.ConselhoOffice?.setReplay(frame); historical=true;
    document.body.dataset.replaying='true'; $('office-replay-banner').hidden=false;
    $('office-replay-label').textContent='Histórico · conversa #'+recording.run.source_event_id+' · '+(position<0?'início':'etapa '+(position+1)+'/'+recording.events.length);
    updateControls();
  }
  function tick() {
    if(!playing||document.hidden)return;
    if(position>=recording.events.length-1){pause();return;}
    showPosition(position+1);
    if(position>=recording.events.length-1){pause();return;}
    timer=setTimeout(tick,900/Number($('activity-speed').value));
  }
  function play() {
    if(!recording?.events.length)return;
    if(playing){pause();return;}
    if(position>=recording.events.length-1)position=-1;
    playing=true;tick();
  }
  function appendRuns(runs) {
    const fragment=document.createDocumentFragment();
    for(const run of runs){
      if(knownRuns.has(run.source_event_id))continue;
      knownRuns.add(run.source_event_id);
      const li=node('li'),button=node('button',undefined,'activity-run');button.type='button';button.dataset.run=run.source_event_id;
      const head=node('span',undefined,'activity-run-head');
      head.append(node('strong',run.title),node('span',status(run.status)));
      const participants=run.agent_ids.map(id=>names.get(id)||id).join(', ');
      const foot=node('span',undefined,'activity-run-foot');
      const time=node('time',stamp(run.created_at));time.dateTime=run.created_at;
      foot.append(node('span',channel(run.channel)+' · #'+run.source_event_id),time);
      button.append(head,node('span',participants||'Aguardando registro de um agente','activity-run-participants'),foot);
      li.append(button);fragment.append(li);
    }
    $('activity-runs').append(fragment);
  }
  async function loadHistory(reset=true) {
    exit(); recording=null;
    if(reset){knownRuns=new Set();nextBefore=null;$('activity-runs').replaceChildren();}
    $('activity-detail').hidden=true; $('activity-list-view').hidden=false;
    $('activity-empty').hidden=true; $('activity-more').disabled=true;
    const attempt=begin(), agent=$('activity-agent').value;
    retry=()=>loadHistory(reset);
    const query=new URLSearchParams({limit:'12'});if(agent)query.set('agent',agent);if(!reset&&nextBefore)query.set('before',nextBefore);
    try{
      const data=await request('/api/office/history?'+query,attempt.signal);
      if(attempt.generation!==generation)return;
      appendRuns(data.runs);nextBefore=data.next_before;
      $('activity-more').hidden=!data.has_more;
      $('activity-empty').hidden=knownRuns.size>0;
      $('activity-empty-title').textContent=agent ? (names.get(agent)+' ainda não tem atividades registradas') : 'Nenhuma atividade registrada';
      $('activity-refresh').textContent='Atualizar';delete $('activity-refresh').dataset.hasNew;
    }catch(error){fail(error,attempt.generation);}
    finally{if(attempt.generation===generation){$('activity-loading').hidden=true;$('activity-more').disabled=false;}}
  }
  function detail(data) {
    $('activity-detail-title').textContent='Conversa #'+data.run.source_event_id;
    $('activity-detail-meta').textContent=channel(data.run.channel)+' · '+stamp(data.run.created_at);
    const summary=$('activity-summary');summary.replaceChildren();
    const pairs=[
      ['Registro',status(data.run.status)],
      ['Personagens',data.run.agent_ids.map(id=>names.get(id)||id).join(', ')||'Sem atividade de agente'],
      ['Duração registrada',data.run.duration_ms===null?'Não disponível':data.run.duration_ms===0?'< 1 ms':data.run.duration_ms+' ms'],
      ['Etapas',data.events.length+' de '+data.run.event_count]
    ];
    for(const[label,value]of pairs){const group=node('div');group.append(node('dt',label),node('dd',value));summary.append(group);}
    $('activity-truncated').hidden=!data.truncated;
    const fragment=document.createDocumentFragment();
    data.events.forEach((event,index)=>{
      const li=node('li'),button=node('button',undefined,'activity-event');button.type='button';button.dataset.step=index;
      const head=node('span'),time=node('time',stamp(event.created_at));time.dateTime=event.created_at;
      head.append(node('span',event.title),time);
      button.append(head,node('span',(event.agent_id?names.get(event.agent_id)+' · ':'')+'evento #'+event.sequence));
      li.append(button);fragment.append(li);
    });
    $('activity-events').replaceChildren(fragment);updateControls();
  }
  async function loadDetail(id) {
    if(!Number.isSafeInteger(id)||id<=0)return;
    exit(); const attempt=begin();retry=()=>loadDetail(id);
    try{
      const data=await request('/api/office/replay?source_event_id='+id,attempt.signal);
      if(attempt.generation!==generation)return;
      data.events=model.validateEvents(data.events); recording=data; position=-1;
      detail(data); $('activity-list-view').hidden=true;$('activity-detail').hidden=false;
      $('activity-detail-title').focus({preventScroll:false});
    }catch(error){fail(error,attempt.generation);}
    finally{if(attempt.generation===generation)$('activity-loading').hidden=true;}
  }
  $('activity-agent').addEventListener('change',()=>loadHistory());
  $('activity-refresh').addEventListener('click',()=>loadHistory());
  $('activity-more').addEventListener('click',()=>loadHistory(false));
  $('activity-retry').addEventListener('click',()=>retry?.());
  $('activity-runs').addEventListener('click',event=>{const run=event.target.closest('[data-run]');if(run)loadDetail(Number(run.dataset.run));});
  $('activity-back').addEventListener('click',()=>{
    exit();controller?.abort();generation++;recording=null;$('activity-detail').hidden=true;$('activity-list-view').hidden=false;$('activity-loading').hidden=true;
    $('activity-agent').focus({preventScroll:false});
  });
  $('activity-events').addEventListener('click',event=>{const step=event.target.closest('[data-step]');if(step){pause();showPosition(Number(step.dataset.step));}});
  $('activity-play').addEventListener('click',play);
  $('activity-prev').addEventListener('click',()=>{pause();showPosition(position-1);});
  $('activity-next').addEventListener('click',()=>{pause();showPosition(position+1);});
  $('activity-position').addEventListener('input',()=>{pause();showPosition(Number($('activity-position').value));});
  $('activity-speed').addEventListener('change',()=>{if(playing){clearTimeout(timer);timer=setTimeout(tick,900/Number($('activity-speed').value));}});
  $('activity-live').addEventListener('click',exit);
  $('exit-office-replay').addEventListener('click',exit);
  $('agent-history').addEventListener('click',event=>{
    const selected=window.ConselhoOffice?.getState().selected || participant;
    $('activity-agent').value=names.has(selected)?selected:'';
    window.ConselhoWorkspace.openPanel('activity',event.currentTarget);
  });
  window.addEventListener('office-agent-selected',event=>{participant=event.detail.id;});
  window.addEventListener('workspace-panel-change',event=>{
    if(event.detail.panel==='activity')loadHistory();
    else{pause();controller?.abort();generation++;$('activity-loading').hidden=true;}
  });
  window.addEventListener('office-event',event=>{
    if(!$('activity-panel').hidden&&(!event.detail.payload.agent_id||!$('activity-agent').value||event.detail.payload.agent_id===$('activity-agent').value)){
      $('activity-refresh').textContent='Atualizar · nova atividade';$('activity-refresh').dataset.hasNew='true';
    }
  });
  window.addEventListener('office-replay-mode',event=>{
    if(!event.detail.active){historical=false;$('office-replay-banner').hidden=true;delete document.body.dataset.replaying;updateControls();}
  });
  document.addEventListener('visibilitychange',()=>{if(document.hidden)pause();});
  window.addEventListener('pagehide',()=>{pause();controller?.abort();});
  updateControls();
  window.ConselhoActivity=Object.freeze({getState:()=>Object.freeze({playing,historical,position,sourceEventId:recording?.run.source_event_id||null,eventCount:recording?.events.length||0,runCount:knownRuns.size}),exitReplay:exit});
})();
