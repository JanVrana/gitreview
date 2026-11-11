'use strict';
/* A searchable dropdown on top of a native <select>.

   The select stays the source of truth (its options and optgroups, value,
   disabled state and 'change' event), the picker only replaces how it looks:
   a button showing the chosen option and a popup with a filter field and the
   options grouped as in the select. Typing filters (every word must appear),
   ↑/↓ move, Enter picks, Esc closes. Call sync() after changing the select's
   options or value from code. */
function makePicker(select, { placeholder = () => '', empty = () => '' } = {}) {
  const btn = document.createElement('button');
  btn.type = 'button';
  btn.className = 'pk-btn';
  btn.setAttribute('aria-haspopup', 'listbox');
  select.hidden = true;
  select.after(btn);

  let pop = null, input = null, list = null, active = -1, items = [];

  const escHtml = s => String(s).replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));

  function sync() {
    const o = select.selectedOptions[0];
    btn.textContent = o ? o.textContent : '';
    btn.title = o ? o.title || o.textContent : '';
    btn.disabled = select.disabled;
  }

  // Every word of the query must appear in the option text (case-insensitive);
  // the first occurrence of each word is highlighted.
  function render() {
    const words = input.value.toLowerCase().split(/\s+/).filter(Boolean);
    let html = '', group = null;
    items = [];
    for (const o of select.options) {
      const text = o.textContent, low = text.toLowerCase();
      // while searching, unavailable options are left out
      if (words.length && (o.disabled || !words.every(w => low.includes(w)))) continue;
      const g = o.parentElement.tagName === 'OPTGROUP' ? o.parentElement.label : '';
      if (g !== group) {
        group = g;
        if (g) html += `<div class="pk-group">${escHtml(g)}</div>`;
      }
      const marks = [];
      for (const w of words) { const i = low.indexOf(w); marks.push([i, i + w.length]); }
      marks.sort((a, b) => a[0] - b[0]);
      let lbl = '', pos = 0;
      for (const [a, b] of marks) {
        if (a < pos) continue;
        lbl += escHtml(text.slice(pos, a)) + '<mark>' + escHtml(text.slice(a, b)) + '</mark>';
        pos = b;
      }
      lbl += escHtml(text.slice(pos));
      const cls = ['pk-item'];
      if (o.disabled) cls.push('dis');
      if (o.value === select.value) cls.push('cur');
      html += `<div class="${cls.join(' ')}" data-i="${o.index}" title="${escHtml(o.title || text)}">${lbl}</div>`;
      if (!o.disabled) items.push(o.index);
    }
    list.innerHTML = html || `<div class="pk-empty">${escHtml(empty())}</div>`;
    const cur = items.indexOf(select.selectedIndex);
    setActive(words.length || cur < 0 ? 0 : cur);
  }

  function setActive(i) {
    active = items.length ? Math.max(0, Math.min(items.length - 1, i)) : -1;
    list.querySelectorAll('.pk-item.act').forEach(el => el.classList.remove('act'));
    if (active < 0) return;
    const el = list.querySelector(`.pk-item[data-i="${items[active]}"]`);
    el.classList.add('act');
    el.scrollIntoView({ block: 'nearest' });
  }

  function choose(index) {
    close();
    if (index == null || select.selectedIndex === index) return;
    select.selectedIndex = index;
    sync();
    select.dispatchEvent(new Event('change'));
  }

  function place() {
    const r = btn.getBoundingClientRect();
    const w = Math.max(r.width, 320);
    pop.style.top = r.bottom + 2 + 'px';
    pop.style.left = Math.max(8, Math.min(r.left, innerWidth - w - 8)) + 'px';
    pop.style.width = w + 'px';
    list.style.maxHeight = Math.max(120, innerHeight - r.bottom - 60) + 'px';
  }

  function open() {
    if (pop || select.disabled) return;
    pop = document.createElement('div');
    pop.className = 'pk-pop';
    pop.innerHTML = '<input class="pk-input" spellcheck="false" autocomplete="off"><div class="pk-list" role="listbox"></div>';
    document.body.append(pop);
    input = pop.querySelector('input');
    list = pop.querySelector('.pk-list');
    input.placeholder = placeholder();
    place();
    render();
    input.focus();
    btn.setAttribute('aria-expanded', 'true');
    input.addEventListener('input', render);
    input.addEventListener('keydown', e => {
      if (e.key === 'ArrowDown' || e.key === 'ArrowUp') { e.preventDefault(); setActive(active + (e.key === 'ArrowDown' ? 1 : -1)); }
      else if (e.key === 'PageDown' || e.key === 'PageUp') { e.preventDefault(); setActive(active + (e.key === 'PageDown' ? 10 : -10)); }
      else if (e.key === 'Enter') { e.preventDefault(); if (active >= 0) choose(items[active]); }
      else if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); close(); btn.focus(); }
      else if (e.key === 'Tab') close();
    });
    // mousedown, not click: the input must not lose focus first
    list.addEventListener('mousedown', e => {
      e.preventDefault();
      const el = e.target.closest('.pk-item');
      if (el && !el.classList.contains('dis')) choose(+el.dataset.i);
    });
    // hover only moves the highlight, it never re-renders the list
    list.addEventListener('mousemove', e => {
      const el = e.target.closest('.pk-item:not(.dis)');
      if (el) { const i = items.indexOf(+el.dataset.i); if (i !== active) setActive(i); }
    });
    setTimeout(() => document.addEventListener('mousedown', outside), 0);
    addEventListener('resize', close);
  }

  function outside(e) {
    if (pop && !pop.contains(e.target) && e.target !== btn) close();
  }

  function close() {
    if (!pop) return;
    pop.remove();
    pop = input = list = null;
    document.removeEventListener('mousedown', outside);
    removeEventListener('resize', close);
    btn.setAttribute('aria-expanded', 'false');
  }

  btn.addEventListener('click', () => (pop ? close() : open()));
  btn.addEventListener('keydown', e => {
    // typing on the focused button opens the search with that character
    if (e.key.length === 1 && !e.ctrlKey && !e.metaKey && !e.altKey) {
      e.preventDefault();
      open();
      if (input) { input.value = e.key; render(); }
    } else if (e.key === 'ArrowDown' && !e.altKey) { e.preventDefault(); open(); }
  });
  sync();
  return { sync, open, close, button: btn };
}
