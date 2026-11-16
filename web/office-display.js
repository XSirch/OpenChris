(function (root, factory) {
  'use strict';
  const display = factory();
  if (typeof module === 'object' && module.exports) module.exports = display;
  if (root) root.ConselhoOfficeDisplay = display;
})(typeof globalThis !== 'undefined' ? globalThis : this, function () {
  'use strict';
  function project(data) {
    if (!data || data.mode !== 'manual') return null;
    const text = (key,fallback) => typeof data[key] === 'string' && data[key].trim() ? data[key] : fallback;
    return {
      wealth:text('net_worth_text','Não informado'), expenses:text('expense_text','Sem lançamentos'),
      pending:text('pending_text','Sem pendências'), category:text('top_category','Sem categoria'),
      categoryAmount:text('top_category_text',''), activity:text('activity_text','Sem atividade registrada'),
      month:text('month_label','Registros manuais')
    };
  }
  function draw(ctx,screen,summary,connection) {
    const data = summary || project({mode:'manual'});
    ctx.save();
    ctx.transform(1,screen.shearY,0,1,screen.x,screen.y);
    ctx.beginPath(); ctx.rect(0,0,screen.width,screen.height); ctx.clip();
    ctx.fillStyle = '#091c2c'; ctx.fillRect(0,0,screen.width,screen.height);
    const text = (value,x,y,size,color,width,weight=600) => {
      ctx.fillStyle = color; ctx.font = weight + ' ' + size + 'px Manrope, sans-serif';
      while (size > 7 && ctx.measureText(value).width > width) { size -= 0.5; ctx.font = weight + ' ' + size + 'px Manrope, sans-serif'; }
      ctx.fillText(value,x,y,width);
    };
    text('PATRIMÔNIO LÍQUIDO',8,13,8,'#b0c4d0',117);
    text(connection === 'live' ? 'AO VIVO' : connection === 'connecting' ? 'ABRINDO' : 'SEM SINAL',128,13,6,'#8bcebb',40);
    text(data.wealth,8,34,18,'#f9efdc',screen.width-16,750);
    ctx.fillStyle = '#294452'; ctx.fillRect(8,42,screen.width-16,1);
    text('GASTOS DO MÊS',8,54,7,'#b0c4d0',83);
    text('PENDENTES',98,54,7,'#b0c4d0',69);
    text(data.expenses,8,68,12,'#efbd72',83,700);
    text(data.pending,98,68,12,'#f9efdc',69,700);
    text(data.category,8,82,8,'#8bcebb',screen.width-16);
    text(data.activity,8,92,7,'#b0c4d0',screen.width-16);
    ctx.restore();
  }
  return Object.freeze({project,draw});
});
