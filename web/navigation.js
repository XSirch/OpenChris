(() => {
  'use strict';
  const menu = document.querySelector('.nav-more');
  const mobile = matchMedia('(max-width:640px)');
  const archive = document.querySelector('.report-archive-disclosure');
  const narrow = matchMedia('(max-width:760px)');
  const adaptMenu = () => { if (menu) menu.open = !mobile.matches; };
  const adaptArchive = () => { if (archive) archive.open = !narrow.matches || location.hash === '#archive-title'; };
  adaptMenu(); adaptArchive();
  mobile.addEventListener('change', adaptMenu);
  narrow.addEventListener('change', adaptArchive);
  document.addEventListener('click', event => {
    if (mobile.matches && menu?.open && (!menu.contains(event.target) || event.target.closest('.nav-more-list a'))) menu.open = false;
  });
  document.addEventListener('keydown', event => {
    if (event.key === 'Escape' && mobile.matches && menu?.open) {
      menu.open = false; menu.querySelector('summary').focus(); event.preventDefault();
    }
  });
  window.addEventListener('hashchange', () => {
    if (location.hash === '#archive-title' && archive) archive.open = true;
  });
})();
