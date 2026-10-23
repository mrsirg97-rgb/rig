(() => {
  const sel = 'a,button,input,select,textarea,summary,[role=button],[role=link],[role=textbox],[role=checkbox],[role=radio],[role=combobox],[contenteditable=true],[onclick]';
  const text = (el) => (el.innerText || el.textContent || '').trim();
  const role = (el) => {
    const explicit = (el.getAttribute('role') || '').trim();
    if (explicit) return explicit;
    const tag = el.tagName.toLowerCase();
    if (tag === 'button' || tag === 'summary') return 'button';
    if (tag === 'a') return el.hasAttribute('href') ? 'link' : 'button';
    if (tag === 'select') return 'combobox';
    if (tag === 'textarea') return 'textbox';
    if (tag === 'input') {
      const t = (el.type || 'text').toLowerCase();
      if (t === 'checkbox') return 'checkbox';
      if (t === 'radio') return 'radio';
      if (t === 'submit' || t === 'button' || t === 'reset') return 'button';
      return 'textbox';
    }
    return tag;
  };
  const labelOf = (el) => {
    const by = el.getAttribute('aria-labelledby');
    if (by) {
      const names = by.split(/\s+/).map(id => {
        const n = document.getElementById(id);
        return n ? text(n) : '';
      }).join(' ').trim();
      if (names) return names;
    }
    const direct = el.getAttribute('aria-label') || el.getAttribute('alt') || el.getAttribute('title') || el.getAttribute('placeholder');
    if (direct && direct.trim()) return direct.trim();
    if (el.labels && el.labels.length) {
      const l = text(el.labels[0]);
      if (l) return l;
    }
    const t = text(el);
    if (t) return t;
    return (el.value || '').trim();
  };
  const clean = (s) => String(s).replace(/[\u0000-\u001f\u007f]/g, ' ').replace(/\s+/g, ' ').trim().slice(0, 80);
  const out = [];
  const refs = [];
  for (const el of document.querySelectorAll(sel)) {
    if (el.disabled) continue;
    const rect = el.getBoundingClientRect();
    if (rect.width <= 0 || rect.height <= 0) continue;
    const style = getComputedStyle(el);
    if (style.visibility === 'hidden' || style.display === 'none' || style.opacity === '0') continue;
    refs.push(el);
    out.push({role: clean(role(el)), name: clean(labelOf(el))});
  }
  window.__rigRefs = refs;
  return JSON.stringify(out);
})()
