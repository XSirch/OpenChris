(() => {
  'use strict';
  document.querySelectorAll('[data-finance-form]').forEach(form => {
    let saving = false;
    form.addEventListener('submit', async event => {
      event.preventDefault();
      if (saving || !form.reportValidity()) return;
      const feedback = form.querySelector('.finance-form-feedback');
      const button = form.querySelector('[type=submit]'), original = button.textContent;
      const body = new URLSearchParams(new FormData(form));
      saving = true; button.disabled = true; button.textContent = 'Salvando…';
      if (feedback) feedback.textContent = '';
      try {
        const response = await fetch(form.getAttribute('action'),{method:'POST',credentials:'same-origin',headers:{'Accept':'application/json','Content-Type':'application/x-www-form-urlencoded'},body});
        let result;
        try { result = await response.json(); }
        catch { throw new Error('O servidor não conseguiu confirmar o envio. Seus campos foram mantidos; tente novamente.'); }
        if (!response.ok) {
          if (response.status === 401) { location.assign('/login'); return; }
          throw new Error(result.error || 'Não foi possível salvar. Seus campos foram mantidos.');
        }
        location.assign(result.redirect);
      } catch (error) {
        if (feedback) feedback.textContent = error instanceof TypeError ? 'A conexão falhou. Seus campos foram mantidos. Tente salvar novamente.' : error.message;
        saving = false; button.disabled = false; button.textContent = original;
      }
    });
  });
  // Native fragment links should also open their disclosure for keyboard/no-JS readers.
  document.querySelectorAll('a[href="#new-expense"]').forEach(link => link.addEventListener('click', () => {
    const details = document.getElementById('new-expense');
    if (details) { details.open = true; details.querySelector('input:not([type=hidden])')?.focus(); }
  }));
})();
