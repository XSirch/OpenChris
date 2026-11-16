(function (root, factory) {
  'use strict';
  const model = factory();
  if (typeof module === 'object' && module.exports) module.exports = model;
  if (root) root.ConselhoReplayModel = model;
})(typeof globalThis !== 'undefined' ? globalThis : this, function () {
  'use strict';
  const templates = Object.freeze({
    'conversation.received':{state:'pending', title:'Conversa recebida'},
    'agent.started':{state:'running', title:'Organização iniciada', work:'running'},
    'memory.episode_saved':{state:'complete', title:'Memória registrada'},
    'agent.completed':{state:'idle', title:'Organização concluída', work:'available'},
    'agent.failed':{state:'error', title:'Organização interrompida', work:'failed'}
  });
  const fields = new Set(['sequence','event_type','agent_id','state','source_event_id','channel','created_at','title']);
  const positive = value => Number.isSafeInteger(value) && value > 0;
  const agentId = value => typeof value === 'string' && /^A(0[1-9]|1[0-9]|2[0-3])$/.test(value);

  function validDate(value) {
    if (typeof value !== 'string') return false;
    const parts = /^(\d{4})-(\d{2})-(\d{2})T([01]\d|2[0-3]):([0-5]\d):([0-5]\d)(?:\.\d{1,9})?(Z|[+-](?:[01]\d|2[0-3]):[0-5]\d)$/.exec(value);
    if (!parts || !Number.isFinite(Date.parse(value))) return false;
    const year = Number(parts[1]), month = Number(parts[2]), day = Number(parts[3]);
    const leap = year % 4 === 0 && (year % 100 !== 0 || year % 400 === 0);
    const days = [31,leap ? 29 : 28,31,30,31,30,31,31,30,31,30,31];
    return month >= 1 && month <= 12 && day >= 1 && day <= days[month-1];
  }

  function validateEvents(events) {
    if (!Array.isArray(events) || events.length > 200) throw new Error('O replay aceita uma lista de até 200 eventos.');
    let source = null, channel = null;
    const ordered = events.map(event => {
      if (!event || typeof event !== 'object' || Array.isArray(event) || Object.keys(event).some(key => !fields.has(key))) throw new Error('Evento de replay inválido.');
      if (!positive(event.sequence)) throw new Error('A sequência do evento é inválida.');
      if (!positive(event.source_event_id) || (source !== null && event.source_event_id !== source)) throw new Error('A execução do replay é inválida.');
      const template = Object.hasOwn(templates,event.event_type) ? templates[event.event_type] : null;
      if (!template || event.state !== template.state) throw new Error('O tipo ou estado do evento é inválido.');
      if (event.title !== template.title) throw new Error('O título do evento de replay é inválido.');
      if (!['telegram','dashboard'].includes(event.channel) || (channel !== null && event.channel !== channel)) throw new Error('O canal da execução é inválido.');
      if (!validDate(event.created_at)) throw new Error('A data do evento de replay é inválida.');
      if (event.agent_id !== undefined && (!agentId(event.agent_id) || !template.work)) throw new Error('O agente do evento é inválido.');
      source = event.source_event_id; channel = event.channel;
      const cloned = {sequence:event.sequence, event_type:event.event_type, state:event.state, source_event_id:event.source_event_id,
        channel:event.channel, created_at:event.created_at, title:event.title};
      if (event.agent_id !== undefined) cloned.agent_id = event.agent_id;
      return cloned;
    }).sort((a,b) => a.sequence-b.sequence);
    if (ordered.some((event,index) => index > 0 && event.sequence <= ordered[index-1].sequence)) throw new Error('A sequência do replay contém eventos repetidos.');
    return ordered;
  }

  function stateAt(events, index) {
    const ordered = validateEvents(events);
    if (!Number.isSafeInteger(index) || index < -1 || index >= ordered.length) throw new Error('O índice da etapa do replay é inválido.');
    const states = {};
    for (let position = 0; position <= index; position++) {
      const event = ordered[position], state = templates[event.event_type].work;
      if (state && event.agent_id) states[event.agent_id] = {state, task:event.title};
    }
    const current = ordered[index];
    return {states, sequence:current ? current.sequence : null, created_at:current ? current.created_at : null, index};
  }

  return Object.freeze({validateEvents,stateAt});
});
