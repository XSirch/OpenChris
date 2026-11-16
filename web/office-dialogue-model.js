(function (root, factory) {
  'use strict';
  const model = factory();
  if (typeof module === 'object' && module.exports) module.exports = model;
  if (root) root.ConselhoDialogueModel = model;
})(typeof globalThis !== 'undefined' ? globalThis : this, function () {
  'use strict';
  const stages = ['Pesquisa pública','Estratégia','Risco','Crítica independente','Síntese'];
  const tasksByStage = ['Pesquisando fontes públicas','Preparando a proposta','Avaliando os riscos da proposta','Fazendo a crítica independente','Reunindo propostas, riscos e objeções'];
  const agentID = /^A(0[1-9]|1[0-9]|2[0-3])$/;
  const open = run => ['pending','running'].includes(run.status);
  const validTask = task => agentID.test(task.agent_id) && Number.isInteger(task.stage) && task.stage >= 0 && task.stage < stages.length;
  const reportLink = task => uuid.test(task.run_id) ? '/reports?run='+task.run_id+'#task-'+task.stage : undefined;
  const waitingText = (code,paused) => paused ? 'Acompanhamento pausado' : ['waiting_budget','rate_limited','daily_limit'].includes(code) ? 'Aguardando cota' :
    code === 'not_authenticated' ? 'Aguardando login do Codex' : code === 'unavailable' ? 'Aguardando disponibilidade do Codex' : 'Na fila para a próxima etapa';
  const uuid = /^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$/i;
  function excerpt(value, limit = 170) {
    if (typeof value !== 'string') return '';
    const chars = Array.from(value.replace(/\s+/gu,' ').trim());
    return chars.length > limit ? chars.slice(0,limit).join('').trimEnd() + '…' : chars.join('');
  }
  function project(detail, names = {}) {
    if (!detail?.run || !uuid.test(detail.run.id) || !Array.isArray(detail.tasks)) return null;
    const runId = detail.run.id, messages = [], tasks = [...detail.tasks].sort((a,b)=>a.stage-b.stage);
    let active = null;
    for (const task of tasks) {
      if (!validTask(task) || task.run_id !== runId) continue;
      const base = {agentId:task.agent_id,name:names[task.agent_id],stage:task.stage,role:stages[task.stage],href:'/reports?run='+runId+'#task-'+task.stage};
      if (task.status === 'running' && detail.run.status === 'running') active = {...base,at:task.started_at};
      if (task.status !== 'complete') continue;
      const text = excerpt(task.stage === 0 ? task.result?.summary : task.result?.reply);
      if (!text) continue;
      messages.push({...base,text,at:task.completed_at || task.started_at || detail.run.created_at});
    }
    return {runId,historical:!['pending','running'].includes(detail.run.status),at:detail.run.created_at,messages,active};
  }
  function place(anchor, viewport, width, height) {
    if (!anchor || !viewport || width+16 > viewport.width || anchor.x < 0 || anchor.x > viewport.width || anchor.y < 0 || anchor.y > viewport.height || height+80 > viewport.height) return null;
    const clamp = (n,min,max) => Math.min(max,Math.max(min,n));
    const left = clamp(anchor.x-width/2,8,viewport.width-width-8);
    const top = clamp(anchor.y-height-20,8,viewport.height-height-64);
    return {left,top,tail:clamp(anchor.x-left,16,width-16),above:top+height<=anchor.y};
  }
  function heartbeatDeadline(status, now) {
    const heartbeat = Date.parse(status?.heartbeat_at);
    return status?.enabled && heartbeat <= now+180000 ? heartbeat+180000 : 0;
  }
  function runningTasks(status, now = Date.now()) {
    if (!(heartbeatDeadline(status,now) > now)) return [];
    const runs = new Set((status.runs || []).filter(run => run.status === 'running').map(run => run.id));
    return (status.tasks || []).filter(task => validTask(task) && runs.has(task.run_id) && task.status === 'running' && Date.parse(task.lease_expires_at)>now);
  }
  function activities(status, jobs, now = Date.now()) {
    const result = {};
    const runs = new Set((status?.runs || []).filter(open).map(run => run.id));
    const tasks = (status?.tasks || []).filter(task => validTask(task) && runs.has(task.run_id));
    if (status?.enabled) {
      for (const task of tasks) {
        if (task.status === 'pending') result[task.agent_id] ??= {state:'queued',task:waitingText(task.error_code,status.paused),href:reportLink(task)};
        if (task.status === 'running') result[task.agent_id] = {state:'unconfirmed',task:'Execução sem confirmação',href:reportLink(task)};
      }
    }
    for (const job of jobs) {
      if (!agentID.test(job.agent_id)) continue;
      if (job.status === 'pending') result[job.agent_id] = {state:'queued',task:waitingText(job.error_code,false)};
      if (job.status === 'running') result[job.agent_id] = {state:'unconfirmed',task:'Execução sem confirmação'};
    }
    for (const task of runningTasks(status,now)) {
      if (task.stage >= 2) {
        const reviewedStages = task.stage === 4 ? [1,2,3] : [1];
        for (const prior of tasks) {
          if (prior.run_id !== task.run_id || prior.status !== 'complete' || !reviewedStages.includes(prior.stage)) continue;
          result[prior.agent_id] = {state:'review',seat:'meeting',key:task.id,task:prior.stage === 1 ? 'Proposta em revisão' : 'Parecer em revisão',href:reportLink(prior)};
        }
      }
      result[task.agent_id] = {state:'running',seat:task.stage >= 2 ? 'meeting' : 'workstation',key:task.id,
        task:status.paused ? 'Acompanhamento pausado. O resultado da execução em curso será descartado.' : tasksByStage[task.stage],href:reportLink(task)};
    }
    for (const job of jobs) {
      if (agentID.test(job.agent_id) && job.status === 'running' && Date.parse(job.lease_expires_at)>now) {
        result[job.agent_id] = {state:'running',seat:'workstation',key:job.source_event_id,task:'Analisando a conversa'};
      }
    }
    return result;
  }
  return Object.freeze({project,excerpt,place,runningTasks,activities,heartbeatDeadline});
});
