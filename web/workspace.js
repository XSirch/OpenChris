(() => {
  'use strict';
  document.body.classList.remove('no-js');
  const $ = id => document.getElementById(id);
  const reduced = matchMedia('(prefers-reduced-motion: reduce)');
  const drawers = [...document.querySelectorAll('[data-drawer]')];
  let trigger = null, active = null;
  const currency = cents => new Intl.NumberFormat('pt-BR',{style:'currency',currency:'BRL',maximumFractionDigits:0}).format(cents/100);
  function closeDrawers(restore = true) {
    drawers.forEach(panel => { panel.hidden = true; });
    document.querySelectorAll('[data-open-panel][aria-expanded]').forEach(button => button.setAttribute('aria-expanded','false'));
    delete document.body.dataset.drawerOpen; active = null;
    window.dispatchEvent(new CustomEvent('workspace-panel-change',{detail:{panel:null}}));
    if (restore) {
      const fallback = document.querySelector('[data-open-panel=home]');
      const target = trigger?.isConnected && trigger.getClientRects().length ? trigger : fallback;
      target?.focus({preventScroll:true});
    }
  }
  function selectView(name, focus = false) {
    document.querySelectorAll('.overview-tabs [role=tab]').forEach(tab => {
      const selected = tab.dataset.view === name;
      tab.setAttribute('aria-selected',String(selected)); tab.tabIndex = selected ? 0 : -1;
      $(tab.getAttribute('aria-controls')).hidden = !selected;
      if (selected && focus) tab.focus();
    });
  }
  function openPanel(name, source) {
    if (['home','history','goals'].includes(name)) {
      closeDrawers(false); selectView(name === 'home' ? 'summary' : name,name !== 'home');
      document.querySelectorAll('.rail-item[data-open-panel]').forEach(button => button.classList.toggle('is-active',button.dataset.openPanel === name));
      if (innerWidth <= 640) document.querySelector('.finance-sidebar').scrollIntoView({behavior:reduced.matches?'instant':'smooth',block:'start'});
      return;
    }
    const panel = drawers.find(item => item.dataset.drawer === name); if (!panel) return;
    if (active === name && source?.getAttribute('aria-expanded') === 'true') { closeDrawers(); return; }
    const nextTrigger = source?.closest('[data-drawer]') ? trigger : source || document.activeElement;
    closeDrawers(false); trigger = nextTrigger; active = name;
    panel.hidden = false; document.body.dataset.drawerOpen = name;
    window.dispatchEvent(new CustomEvent('workspace-panel-change',{detail:{panel:name}}));
    document.querySelectorAll('[data-open-panel][aria-expanded]').forEach(button => button.setAttribute('aria-expanded',String(button.dataset.openPanel === name)));
    const focus = name === 'conversation' && $('message') && !$('message').disabled ? $('message') : name === 'agents' ? $('agent-search') : name === 'memory' ? $('tab-facts') : panel.querySelector('h2');
    if (focus) { if (/^H/.test(focus.tagName)) focus.tabIndex = -1; focus.focus({preventScroll:true}); }
  }
  document.addEventListener('click',event => {
    const opener = event.target.closest('[data-open-panel]');
    if (opener) { event.preventDefault(); openPanel(opener.dataset.openPanel,opener); }
    if (event.target.closest('[data-edit-wealth]')) {
      closeDrawers(false);
      const editor = $('net-worth-editor'); editor.open = true;
      editor.scrollIntoView({behavior:reduced.matches ? 'instant' : 'smooth',block:'center'});
      $('net-worth-assets').focus({preventScroll:true});
    }
    if (event.target.closest('[data-close-panel]')) closeDrawers();
    const featured = event.target.closest('[data-select-agent]');
    if (featured && window.ConselhoOffice) { closeDrawers(false); window.ConselhoOffice.selectAgent(featured.dataset.selectAgent,featured); }
    if (event.target.closest('#agent-roster .agent-roster-item')) closeDrawers(false);
    if (event.target.closest('[data-prompt]')) openPanel('conversation',event.target.closest('[data-prompt]'));
    if (event.target.closest('#session-exit')) $('logout').click();
  });
  document.addEventListener('keydown',event => {
    if (event.defaultPrevented) return;
    if (event.key === 'Escape' && active) { event.preventDefault(); closeDrawers(); }
  });
  window.addEventListener('workspace-open',event => openPanel(event.detail.panel,event.detail.source));
  document.querySelector('.overview-tabs').addEventListener('click',event => {
    const tab = event.target.closest('[role=tab]'); if (tab) selectView(tab.dataset.view);
  });
  document.querySelector('.overview-tabs').addEventListener('keydown',event => {
    if (!['ArrowLeft','ArrowRight','Home','End'].includes(event.key)) return;
    event.preventDefault(); const tabs = [...document.querySelectorAll('.overview-tabs [role=tab]')];
    const current = tabs.findIndex(tab => tab.getAttribute('aria-selected') === 'true');
    const index = event.key === 'Home' ? 0 : event.key === 'End' ? tabs.length-1 : (current + (event.key === 'ArrowLeft' ? -1 : 1) + tabs.length) % tabs.length;
    selectView(tabs[index].dataset.view,true);
  });
  const rows = [...document.querySelectorAll('[data-cents]')];
  const values = rows.map(cell => Number(cell.dataset.cents)), maximum = Math.max(...values);
  const chart = $('wealth-chart');
  rows.forEach((cell,index) => {
    const cents = values[index], label = cell.closest('tr').querySelector('th').textContent;
    cell.textContent = currency(cents);
    const bar = document.createElement('div'); bar.className = 'wealth-bar'; bar.dataset.label = label;
    bar.style.height = Math.max(12,(cents/maximum-.65)*280) + '%';
    bar.title = label + ': ' + currency(cents); bar.setAttribute('aria-hidden','true'); chart?.append(bar);
  });
  function portraits() {
    document.querySelectorAll('[data-agent-portrait]').forEach(target => {
      const source = document.querySelector('#agent-roster [data-agent="'+target.dataset.agentPortrait+'"] canvas');
      if (!source?.width) return;
      const ctx = target.getContext('2d'); ctx.clearRect(0,0,target.width,target.height); ctx.imageSmoothingEnabled = false;
      const scale = Math.min(target.width/source.width,target.height/source.height), w=source.width*scale,h=source.height*scale;
      ctx.drawImage(source,(target.width-w)/2,target.height-h,w,h);
    });
  }
  window.addEventListener('office-ready',portraits);
  if (window.ConselhoOffice?.getState().ready) portraits();
  window.addEventListener('conselho-state',event => {
    const reserve = $('overview-reserve');
    if (!reserve) return;
    const fact = event.detail.facts.find(item => item.predicate === 'reserve_months') || event.detail.facts[0];
    if (fact) reserve.textContent = fact.value + ' meses';
    else reserve.textContent = 'Não declarada';
  });
  const exit = document.createElement('button'); exit.id = 'session-exit'; exit.type = 'button'; exit.className = 'button-quiet'; exit.textContent = 'Sair do conselho';
  $('connections-panel').append(exit);
  document.addEventListener('DOMContentLoaded',() => {
    const aliases = {'history-pane':'history','goals-pane':'goals','memory-panel':'memory','characters-panel':'agents','activity-panel':'activity','connections-panel':'connections'};
    const fragment = location.hash.slice(1), initialPanel = aliases[fragment] || fragment;
    if (['home','history','goals','memory','agents','activity','connections'].includes(initialPanel)) openPanel(initialPanel);
  },{once:true});
  window.ConselhoWorkspace = Object.freeze({openPanel,closePanels:closeDrawers});
})();
