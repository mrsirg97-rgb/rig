// the dashboard's client: the fetch loop, the TUI's shapes, the forms, the
// forge's editor, the folder browser. Plain JS, no framework, no build
// step, no external asset. The token rides the cookie (set once by the
// first ?token= load); same-origin fetch carries it.

const nav = document.getElementById('nav');
const main = document.getElementById('main');
const cwdSelect = document.getElementById('cwd');
const cwdAdd = document.getElementById('cwd-add');
const backdrop = document.getElementById('backdrop');
const navToggle = document.getElementById('nav-toggle');
const browseBtn = document.getElementById('browse-btn');
const browser = document.getElementById('browser');

const state = { view: 'sessions', cwd: '', transcript: null, pluginZone: 'approved' };

// the TUI's glyph table (frontend/tui theme.go), unicode set.
const G = {
  pending: '○', active: '◐', done: '●', fail: '✕', ok: '✓',
  compact: '⧉', prompt: '❯', barOn: '▰', barOff: '▱', dot: '·', dir: '▸',
};

async function api(path, opts) {
  const res = await fetch(path, opts);
  if (!res.ok) {
    let msg = res.status + ' ' + res.statusText;
    try {
      const j = await res.json();
      if (j && j.error) msg = j.error;
    } catch (_) {}
    const err = new Error(msg);
    err.status = res.status;
    throw err;
  }
  return res.json();
}

function post(path, body) {
  return api(path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
}

function el(tag, cls, text) {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text !== undefined) e.textContent = text;
  return e;
}

function span(text, cls) { return el('span', cls, text); }
function line(cls) { return el('div', 'line' + (cls ? ' ' + cls : '')); }
function note(text, cls) { return el('p', cls, text); }
function clear() { main.innerHTML = ''; }

function renderError(msg) {
  clear();
  main.appendChild(note(G.fail + ' ' + msg, 'error'));
}

function verbatimEl(text) {
  const pre = el('pre', 'verbatim', text);
  return pre;
}

function button(label, cls, onClick) {
  const b = el('button', cls, label);
  b.type = 'button';
  if (onClick) b.addEventListener('click', onClick);
  return b;
}

// the tool block: the TUI's commit shape — the opening ● name · detail,
// the body, and the closing name ✓ (frontend/tui commit.go).
function toolBlock(name, detail) {
  const tb = el('div', 'tb');
  const open = el('div', 'tb-open');
  open.appendChild(span(G.done + ' ', 'accent'));
  open.appendChild(span(name, 'accent'));
  if (detail) {
    open.appendChild(span(' ' + G.dot + ' ', 'dim'));
    open.appendChild(span(detail, 'text'));
  }
  const body = el('div', 'tb-body');
  const close = el('div', 'tb-close');
  close.appendChild(span(name + ' ', 'dim'));
  close.appendChild(span(G.ok, 'ok'));
  tb.appendChild(open);
  tb.appendChild(body);
  tb.appendChild(close);
  return { el: tb, body, open, close };
}

function setClose(tb, outcome, slot) {
  tb.close.innerHTML = '';
  tb.close.appendChild(span(tb.open.children[1].textContent + ' ', 'dim'));
  tb.close.appendChild(span(outcome, slot));
}

function echoEl() {
  const e = el('div', 'echo');
  e.hidden = true;
  return e;
}

function setEcho(e, text, isError) {
  e.hidden = false;
  e.textContent = (isError ? G.fail + ' ' : '→ ') + text;
  e.classList.toggle('err', !!isError);
}

// the prompt row: ❯ label input (the TUI's input line).
function promptRow(label, input, mark) {
  const d = el('div', 'prompt');
  d.appendChild(span(mark || G.prompt, 'mark' + (mark ? ' dim' : '')));
  if (label) d.appendChild(span(label, 'label'));
  d.appendChild(input);
  return d;
}

function textInput(placeholder, required) {
  const i = el('input');
  i.type = 'text';
  i.placeholder = placeholder;
  i.spellcheck = false;
  i.autocomplete = 'off';
  if (required) i.required = true;
  return i;
}

// --- the TUI's todo render, mirrored (frontend/tui tools_render.go) ---

function parseTodo(reply) {
  const lines = reply.replace(/\n+$/, '').split('\n');
  let i = 0;
  let action = '';
  if (i < lines.length && lines[i].startsWith('→ ')) { action = lines[i].slice(2); i++; }
  if (i >= lines.length) return null;
  const m = lines[i].match(/^(\d+)\/(\d+) done(?: · next: (\S+))?(?: · (\d+) failed)?$/);
  if (!m) return null;
  const p = {
    action,
    done: +m[1],
    total: +m[2],
    next: m[3] || '',
    failed: m[4] ? +m[4] : 0,
    active: 0,
    tasks: [],
    footer: '',
  };
  i++;
  for (; i < lines.length; i++) {
    const l = lines[i];
    if (l === '') continue;
    if (l.startsWith('· ')) {
      if (p.footer) return null;
      p.footer = l;
      continue;
    }
    const tm = l.match(/^  (t\d+) \[([x!~ ])\] (.+)$/);
    if (!tm) return null;
    const task = { id: tm[1] };
    if (tm[2] === 'x') task.status = 'done';
    else if (tm[2] === '!') task.status = 'failed';
    else if (tm[2] === '~') { task.status = 'active'; p.active++; }
    else task.status = 'pending';
    let rest = tm[3];
    let j = rest.lastIndexOf(' · claimed by ');
    if (j >= 0) { task.claim = rest.slice(j + ' · claimed by '.length); rest = rest.slice(0, j); }
    j = rest.lastIndexOf(' · waits on ');
    if (j >= 0) { task.waits = rest.slice(j + ' · waits on '.length); rest = rest.slice(0, j); }
    task.text = rest;
    p.tasks.push(task);
  }
  return p;
}

function progressBar(done, active, total) {
  let segs = total;
  if (segs > 8) segs = 8;
  if (segs < 1) segs = 1;
  let filled = 0;
  if (total > 0) {
    const n = (done + active) * segs;
    filled = Math.floor(n / total);
    if ((n % total) * 2 >= total) filled++;
  }
  if (filled > segs) filled = segs;
  return { on: filled, off: segs - filled };
}

function taskGlyph(status) {
  switch (status) {
    case 'done': return [G.done, 'ok'];
    case 'active': return [G.active, 'accent'];
    case 'failed': return [G.fail, 'err'];
    default: return [G.pending, 'dim'];
  }
}

function todoBodyEl(p, onVerb) {
  const e = el('div');
  const head = line();
  const bar = progressBar(p.done, p.active, p.total);
  head.appendChild(span(G.barOn.repeat(bar.on), 'bar-on'));
  head.appendChild(span(G.barOff.repeat(bar.off), 'bar-off'));
  let h = ' ' + p.done + '/' + p.total;
  if (p.next) h += ' ' + G.dot + ' next ' + p.next;
  if (p.failed) h += ' ' + G.dot + ' ' + p.failed + ' failed';
  head.appendChild(span(h, 'dim'));
  e.appendChild(head);
  if (p.action) {
    const a = line();
    a.appendChild(span('→ ', 'accent'));
    a.appendChild(span(p.action, 'text'));
    e.appendChild(a);
  }
  for (const t of p.tasks) {
    const l = line();
    const [g, cls] = taskGlyph(t.status);
    l.appendChild(span(g + ' ', cls));
    l.appendChild(span(t.id, 'dim'));
    l.appendChild(span(' ' + t.text, t.status === 'done' ? 'dim' : 'text'));
    if (t.waits) l.appendChild(span(' ' + G.dot + ' waits on ' + t.waits, 'dim'));
    if (t.claim) l.appendChild(span(' ' + G.dot + ' claimed by ' + t.claim, 'dim'));
    if (onVerb && (t.status === 'pending' || t.status === 'active')) {
      const acts = el('span', 'taskact');
      acts.appendChild(span('  ', 'dim'));
      if (t.status === 'pending') acts.appendChild(button('start', null, () => onVerb('start', t.id)));
      acts.appendChild(button('done', null, () => onVerb('complete', t.id)));
      l.appendChild(acts);
    }
    e.appendChild(l);
  }
  if (p.footer) e.appendChild(line('dim')).textContent = p.footer;
  return e;
}

function todoArea(text, onVerb) {
  const parsed = parseTodo(text);
  return parsed ? todoBodyEl(parsed, onVerb) : verbatimEl(text);
}

// --- the TUI's scheduler render, mirrored ---

function parseScheduler(reply) {
  const lines = reply.replace(/\n+$/, '').split('\n');
  if (lines.length === 0) return null;
  if (/^(j\d+) · (\d+) runs? \(oldest first\):$/.test(lines[0])) return { runs: lines };
  const p = { sections: [] };
  let cur = null;
  let job = null;
  for (const l of lines) {
    if (l === '') continue;
    if (l.startsWith('global:') || l.startsWith('cwd:')) {
      job = null;
      cur = {
        name: l.endsWith(': no jobs') ? l.slice(0, -': no jobs'.length) : l.slice(0, -1),
        empty: l.endsWith(': no jobs'),
        jobs: [],
      };
      p.sections.push(cur);
      continue;
    }
    if (!cur) return null;
    if (l.startsWith('  ')) {
      if (!job) return null;
      job.detail.push(l.slice(2));
      continue;
    }
    if (!/^(j\d+)(?: .+)?$/.test(l) || cur.empty) return null;
    job = { head: l, detail: [] };
    cur.jobs.push(job);
  }
  return p;
}

function schedGlyph(head) {
  const f = head.split(/\s+/);
  if (f.includes('active')) return [G.done, 'accent'];
  if (f.includes('paused')) return [G.pending, 'dim'];
  if (f.includes('removed')) return [G.fail, 'err'];
  return [G.pending, 'dim'];
}

function schedulerBodyEl(p) {
  const e = el('div');
  if (p.runs) {
    for (const l of p.runs) e.appendChild(line('dim')).textContent = l;
    return e;
  }
  for (const sec of p.sections) {
    if (sec.empty) {
      e.appendChild(line('dim')).textContent = sec.name + ': no jobs';
      continue;
    }
    e.appendChild(line('dim')).textContent = sec.name + ':';
    for (const j of sec.jobs) {
      const row = line();
      const [g, cls] = schedGlyph(j.head);
      row.appendChild(span(g + ' ', cls));
      row.appendChild(span(j.head, 'text'));
      e.appendChild(row);
      for (const d of j.detail) {
        const dl = line(d.startsWith('drift: ') ? 'detail drift' : 'detail');
        dl.textContent = '  ' + d;
        e.appendChild(dl);
      }
    }
  }
  return e;
}

function schedulerArea(text) {
  const parsed = parseScheduler(text);
  return parsed ? schedulerBodyEl(parsed) : verbatimEl(text);
}

// --- the sessions view ---

function exitGlyph(exit) {
  switch (exit) {
    case 'ok': return [G.ok, 'ok'];
    case 'fault': return [G.fail, 'err'];
    case 'open': return [G.prompt, 'ember'];
    case 'cancelled': return [G.pending, 'dim'];
    default: return [G.dot, 'dim'];
  }
}

async function renderSessions(q) {
  const data = await api('/api/sessions' + q);
  clear();
  const sessions = data.sessions || [];
  const tb = toolBlock('sessions', state.cwd);
  main.appendChild(tb.el);
  if (!sessions.length) {
    tb.body.appendChild(line('dim')).textContent = 'no sessions in this workspace';
    return;
  }
  for (const s of sessions) {
    const row = el('div', 'sess');
    const [g, cls] = exitGlyph(s.exit);
    row.appendChild(span(g + ' ', cls));
    row.appendChild(span(s.id.slice(0, 12), 'text'));
    row.appendChild(span('  ' + s.started.replace('T', ' ').replace(/Z$/, ''), 'dim'));
    row.appendChild(span('  ' + s.turns + ' turns', 'dim'));
    row.appendChild(span('  ' + s.exit, 'exit-' + s.exit));
    row.addEventListener('click', () => renderTranscript(s.id, q));
    tb.body.appendChild(row);
  }
  tb.body.appendChild(el('div', 'hint', 'open a session by clicking its row'));
}

function formatTokens(n) {
  if (n < 1000) return String(n);
  if (n < 10000) return (n / 1000).toFixed(1) + 'k';
  if (n < 1000000) return Math.floor((n + 500) / 1000) + 'k';
  return (n / 1000000).toFixed(1) + 'M';
}

function usageLine(usage) {
  if (!usage || !usage.length) return null;
  let up = 0, down = 0, cache = 0;
  for (const u of usage) { up += u.prompt; down += u.completion; cache += u.cache_read; }
  const hit = up > 0 ? Math.floor(cache * 100 / up) : 0;
  return note('up ' + formatTokens(up) + ' down ' + formatTokens(down) +
    ' ' + G.dot + ' cache r ' + formatTokens(cache) + ' ' + hit + '%', 'dim usage-line');
}

function toolDetail(name, argsStr) {
  let v;
  try { v = JSON.parse(argsStr); } catch (_) { return ''; }
  if (!v || typeof v !== 'object') return '';
  const s = (k) => (typeof v[k] === 'string' ? v[k] : '');
  const first = (x) => { const i = x.indexOf('\n'); return i >= 0 ? x.slice(0, i) : x; };
  switch (name) {
    case 'bash': if (s('command')) return '$ ' + first(s('command')); break;
    case 'read': case 'write': case 'edit': case 'ls': if (s('path')) return s('path'); break;
    case 'find': case 'grep': if (s('pattern')) return s('pattern'); break;
    case 'python': if (s('code')) return first(s('code')); break;
    case 'web_search': if (s('query')) return s('query'); break;
    case 'web_fetch': if (s('url')) return s('url'); break;
    case 'diff': if (s('mode')) return s('mode'); break;
    case 'todo': case 'scheduler': case 'rem': case 'plugins_reload':
      if (s('action')) return s('action') + (s('id') ? ' ' + s('id') : ''); break;
  }
  return '';
}

function messageEl(m) {
  const block = el('div', 'msg msg-' + m.role);
  if (m.role === 'user') {
    const c = el('div', 'content');
    c.appendChild(span(G.prompt + ' ', 'ember'));
    c.appendChild(span(m.content, 'text'));
    block.appendChild(c);
    return block;
  }
  if (m.reasoning) block.appendChild(el('div', 'reasoning', m.reasoning));
  if (m.content && m.role !== 'tool') block.appendChild(el('div', 'content', m.content));
  for (const tc of m.tool_calls || []) {
    const t = el('div', 'tool');
    const head = el('div', 'tool-head');
    head.appendChild(span(G.done + ' ', 'accent'));
    head.appendChild(span(tc.name, 'accent'));
    const d = toolDetail(tc.name, tc.args);
    if (d) {
      head.appendChild(span(' ' + G.dot + ' ', 'dim'));
      head.appendChild(span(d, 'text'));
    }
    t.appendChild(head);
    if (!d) t.appendChild(el('pre', null, tc.args));
    block.appendChild(t);
  }
  if (m.role === 'tool') {
    block.appendChild(el('pre', 'toolres', m.content));
  }
  return block;
}

async function renderTranscript(id, q) {
  state.transcript = id;
  const base = '/api/sessions/' + encodeURIComponent(id) + '/transcript';
  const first = await api(base + q);
  clear();
  const tb = toolBlock('session', id.slice(0, 12) + ' ' + G.dot + ' ' + state.cwd);
  main.appendChild(tb.el);
  const meta = el('div', 'actions');
  meta.appendChild(button('back', null, () => { state.transcript = null; render(); }));
  const count = span('', 'dim');
  meta.appendChild(count);
  tb.body.appendChild(meta);
  const list = el('div');
  tb.body.appendChild(list);
  const more = button('more ↓', null);
  more.disabled = true;
  tb.body.appendChild(more);
  let offset = 0;
  const paint = (d) => {
    for (const m of d.messages) list.appendChild(messageEl(m));
    offset += d.messages.length;
    count.textContent = offset + ' of ' + d.total + ' messages';
    more.disabled = false;
    more.hidden = !d.has_more;
  };
  paint(first);
  more.addEventListener('click', async () => {
    more.disabled = true;
    try {
      paint(await api(base + q + '&limit=' + first.limit + '&offset=' + offset));
    } catch (e) {
      more.disabled = false;
      tb.body.appendChild(note(G.fail + ' ' + e.message, 'error'));
    }
  });
  const u = usageLine(first.usage);
  if (u) tb.body.appendChild(u);
}

// --- the todo view ---

async function renderTodos(q) {
  const data = await api('/api/todo' + q);
  clear();
  let all = false;
  const tb = toolBlock('todo', 'read');
  main.appendChild(tb.el);
  const area = el('div');
  const refresh = async () => {
    const d = await api('/api/todo' + q + '&all=' + all);
    area.innerHTML = '';
    area.appendChild(todoArea(d.text, onVerb));
  };
  async function onVerb(verb, id) {
    try {
      const r = await post('/api/todo/' + verb + q, { id });
      setEcho(out, r.reply);
      await refresh();
    } catch (e) {
      setEcho(out, e.message, true);
    }
  }
  area.appendChild(todoArea(data.text, onVerb));
  tb.body.appendChild(area);

  const actions = el('div', 'actions');
  const tog = button('read all', null, async () => {
    all = !all;
    tog.disabled = true;
    try {
      await refresh();
      tb.open.children[3].textContent = all ? 'read all' : 'read';
      tog.textContent = all ? 'read actionable' : 'read all';
    } catch (e) {
      setEcho(out, e.message, true);
    } finally {
      tog.disabled = false;
    }
  });
  actions.appendChild(tog);
  tb.body.appendChild(actions);

  const ta = el('textarea');
  ta.rows = 2;
  ta.placeholder = 'create: one task per line';
  ta.spellcheck = false;
  tb.body.appendChild(promptRow('', ta));
  const out = echoEl();
  tb.body.appendChild(out);
  const act = el('div', 'actions');
  const btn = button('create', 'primary', async () => {
    if (!ta.value.trim()) { ta.focus(); return; }
    btn.disabled = true;
    try {
      const r = await api('/api/todo' + q, { method: 'POST', headers: { 'Content-Type': 'text/plain' }, body: ta.value });
      setEcho(out, r.reply);
      ta.value = '';
      await refresh();
    } catch (e) {
      setEcho(out, e.message, true);
    } finally {
      btn.disabled = false;
    }
  });
  act.appendChild(btn);
  tb.body.appendChild(act);
}

// --- the scheduler view ---

async function renderScheduler(q) {
  const data = await api('/api/scheduler' + q);
  clear();
  const tb = toolBlock('scheduler', 'list');
  main.appendChild(tb.el);
  const area = el('div');
  area.appendChild(schedulerArea(data.text));
  tb.body.appendChild(area);

  const name = textInput('name (e.g. nightly-digest)', true);
  const prompt = el('textarea');
  prompt.rows = 2;
  prompt.placeholder = 'the prompt the worker runs';
  const cron = textInput('30 7 * * *  (five fields, or once)', true);
  const at = textInput('2026-01-02T03:04:05Z', false);
  const atRow = promptRow('at', at, G.dot);
  atRow.hidden = true;
  cron.addEventListener('input', () => { atRow.hidden = cron.value.trim() !== 'once'; });
  const scope = el('select');
  scope.appendChild(new Option('this workspace (cwd)', 'cwd'));
  scope.appendChild(new Option('global', 'global'));
  tb.body.appendChild(el('div', 'hint', 'create'));
  tb.body.appendChild(promptRow('name', name));
  tb.body.appendChild(promptRow('prompt', prompt, G.dot));
  tb.body.appendChild(promptRow('cron', cron, G.dot));
  tb.body.appendChild(atRow);
  tb.body.appendChild(promptRow('scope', scope, G.dot));
  const out = echoEl();
  tb.body.appendChild(out);
  const act = el('div', 'actions');
  const btn = button('create', 'primary', async () => {
    btn.disabled = true;
    try {
      const body = { name: name.value.trim(), prompt: prompt.value, cron: cron.value.trim() };
      if (body.cron === 'once') body.at = at.value.trim();
      if (scope.value === 'global') body.scope = 'global';
      const r = await post('/api/scheduler' + q, body);
      setEcho(out, r.reply);
      name.value = ''; prompt.value = ''; cron.value = ''; at.value = '';
      const d = await api('/api/scheduler' + q);
      area.innerHTML = '';
      area.appendChild(schedulerArea(d.text));
    } catch (e) {
      setEcho(out, e.message, true);
    } finally {
      btn.disabled = false;
    }
  });
  act.appendChild(btn);
  tb.body.appendChild(act);
}

// --- the models view: the /models table's shape (command/models.go) ---

function effortClass(level) {
  const known = ['off', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max'];
  return known.includes(level) ? 'effort-' + level : 'accent';
}

async function renderModels() {
  const data = await api('/api/models');
  clear();
  const tb = toolBlock('models', 'config');
  main.appendChild(tb.el);
  const models = data.models || [];
  if (!models.length) {
    tb.body.appendChild(line('dim')).textContent = 'no models in the config';
    return;
  }
  for (const m of models) {
    const head = line();
    head.appendChild(span(G.done + ' ', 'accent'));
    head.appendChild(span(m.id, 'text bold'));
    head.appendChild(span('  ' + m.role, 'ember'));
    tb.body.appendChild(head);
    const ctx = line('dim');
    ctx.textContent = '  window ' + m.window + ' ' + G.dot + ' max ' + m.max_tokens + ' ' + G.dot +
      ' reserve ' + m.reserve + ' ' + G.dot + ' keep ' + m.keep_recent + ' ' + G.dot +
      ' trigger ' + (m.window - m.reserve);
    tb.body.appendChild(ctx);
    const eff = line();
    eff.appendChild(span('  effort ', 'dim'));
    const list = m.efforts || [];
    if (!list.length) eff.appendChild(span('(the server default)', 'dim'));
    list.forEach((e, i) => {
      if (i) eff.appendChild(span(' ', 'dim'));
      eff.appendChild(span(e, effortClass(e) + (e === m.effort ? ' effort-on' : '')));
    });
    if (m.effort && !list.includes(m.effort)) eff.appendChild(span('  default ' + m.effort, effortClass(m.effort)));
    tb.body.appendChild(eff);
    tb.body.appendChild(line()).textContent = ' ';
  }
}

// --- the forge: the python editor (a gutter, a highlighted mirror, the textarea) ---

const PY_KW = new Set(('False None True and as assert async await break class continue def del elif ' +
  'else except finally for from global if import in is lambda nonlocal not or pass raise return try ' +
  'while with yield').split(' '));
const PY_BUILTIN = new Set(('print len str int float dict list set tuple range open isinstance getattr ' +
  'hasattr json os sys time re subprocess Exception args').split(' '));

function escapeHTML(s) {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
}

function highlightPython(src) {
  const re = /("""[\s\S]*?"""|'''[\s\S]*?'''|"(?:\\.|[^"\\\n])*"|'(?:\\.|[^'\\\n])*'|#[^\n]*|\b\d+(?:\.\d+)?\b|\b[A-Za-z_]\w*\b|\s+|[^\w\s"'#]+)/g;
  const out = [];
  let prev = '';
  let m;
  while ((m = re.exec(src))) {
    const t = m[0];
    let cls = '';
    if (t[0] === '#') cls = 'hl-c';
    else if (t[0] === '"' || t[0] === "'") cls = 'hl-s';
    else if (/^\d/.test(t)) cls = 'hl-n';
    else if (PY_KW.has(t)) cls = 'hl-k';
    else if (prev === 'def' || prev === 'class') cls = 'hl-f';
    else if (/^[A-Z][A-Z0-9_]+$/.test(t)) cls = 'hl-const';
    else if (PY_BUILTIN.has(t)) cls = 'hl-b';
    out.push(cls ? '<span class="' + cls + '">' + escapeHTML(t) + '</span>' : escapeHTML(t));
    if (!/^\s+$/.test(t)) prev = t;
  }
  return out.join('') + '\n';
}

function editorEl(initial) {
  const wrap = el('div', 'editor');
  const gutter = el('div', 'gutter');
  const cell = el('div', 'cell');
  const pre = el('pre', 'hl');
  pre.setAttribute('aria-hidden', 'true');
  const ta = el('textarea', 'code');
  ta.spellcheck = false;
  ta.setAttribute('autocapitalize', 'off');
  ta.setAttribute('autocomplete', 'off');
  ta.value = initial;
  const sync = () => {
    pre.innerHTML = highlightPython(ta.value);
    const n = ta.value.split('\n').length;
    let g = '';
    for (let i = 1; i <= n; i++) g += i + '\n';
    gutter.textContent = g;
    gutter.scrollTop = ta.scrollTop;
  };
  ta.addEventListener('input', sync);
  ta.addEventListener('scroll', () => {
    pre.scrollTop = ta.scrollTop;
    pre.scrollLeft = ta.scrollLeft;
    gutter.scrollTop = ta.scrollTop;
  });
  ta.addEventListener('keydown', (e) => {
    if (e.key === 'Tab') {
      e.preventDefault();
      const s = ta.selectionStart, t = ta.selectionEnd;
      ta.setRangeText('    ', s, t, 'end');
      sync();
    } else if (e.key === 'Enter') {
      e.preventDefault();
      const s = ta.selectionStart;
      const before = ta.value.slice(0, s);
      const cur = before.slice(before.lastIndexOf('\n') + 1);
      let indent = (cur.match(/^\s*/) || [''])[0];
      if (/:\s*$/.test(cur)) indent += '    ';
      ta.setRangeText('\n' + indent, s, ta.selectionEnd, 'end');
      sync();
    }
  });
  cell.appendChild(pre);
  cell.appendChild(ta);
  wrap.appendChild(gutter);
  wrap.appendChild(cell);
  sync();
  return {
    el: wrap,
    get value() { return ta.value; },
    set value(v) { ta.value = v; sync(); },
    focus: () => ta.focus(),
  };
}

const PLUGIN_TEMPLATE = 'DESCRIPTION = "what it does, one line"\n' +
  'SCHEMA = {"type": "object", "properties": {}}\n\n' +
  'def run(args: dict) -> str:\n' +
  '    return "hello " + str(args.get("who") or "world")\n';

// --- the plugins view: approved | pending, the list, the forge ---

async function renderPlugins() {
  const data = await api('/api/plugins');
  clear();
  const zone = state.pluginZone;
  const tb = toolBlock('plugins', zone);
  main.appendChild(tb.el);

  const seg = el('div', 'seg');
  for (const z of ['approved', 'pending']) {
    const b = button(z + ' ' + (z === 'approved' ? (data.loaded || []).length : (data.pending || []).length), z === zone ? 'active' : '', () => {
      state.pluginZone = z;
      renderPlugins();
    });
    seg.appendChild(b);
  }
  tb.body.appendChild(seg);

  const list = zone === 'approved' ? (data.loaded || []) : (data.pending || []);
  const listEl = el('div');
  if (!list.length) {
    listEl.appendChild(line('dim')).textContent = zone === 'approved'
      ? 'no approved plugins (~/.rig/plugins/)'
      : 'nothing pending (~/.rig/plugins/pending/)';
  }
  for (const p of list) {
    const row = line('click');
    row.appendChild(span((zone === 'approved' ? G.done : G.pending) + ' ', zone === 'approved' ? 'ok' : 'warn'));
    row.appendChild(span(p.name, 'accent'));
    row.appendChild(span(' ' + G.dot + ' ' + (p.description || '(no DESCRIPTION)'), 'dim'));
    row.addEventListener('click', () => openForge(forge, p.name, zone === 'approved' ? 'loaded' : 'pending'));
    listEl.appendChild(row);
  }
  tb.body.appendChild(listEl);
  const hint = el('div', 'hint');
  hint.textContent = zone === 'approved'
    ? 'click a plugin to read it; an edit saves a pending revision, approve (replace) swaps it in'
    : 'click a plugin to edit it; approve moves it into plugins/ — a live session loads it at its next plugins_reload';
  tb.body.appendChild(hint);

  const act = el('div', 'actions');
  act.appendChild(button('new plugin', 'primary', () => openForge(forge, null, 'pending')));
  tb.body.appendChild(act);

  const forge = el('div');
  forge.hidden = true;
  tb.body.appendChild(forge);
}

async function openForge(host, name, zone) {
  host.hidden = false;
  host.innerHTML = '';
  let source = PLUGIN_TEMPLATE;
  if (name) {
    try {
      const d = await api('/api/plugins/source?name=' + encodeURIComponent(name) + '&zone=' + zone);
      source = d.source;
    } catch (e) {
      host.appendChild(note(G.fail + ' ' + e.message, 'error'));
      return;
    }
  }
  const head = line();
  head.appendChild(span(G.prompt + ' ', 'ember'));
  head.appendChild(span(name ? 'forge ' + G.dot + ' ' + name + ' (' + zone + ')' : 'forge ' + G.dot + ' new plugin', 'text'));
  host.appendChild(head);
  const nameIn = textInput('name (lowercase, the filename stem)', true);
  if (name) { nameIn.value = name; nameIn.readOnly = true; }
  host.appendChild(promptRow('name', nameIn, G.dot));
  const ed = editorEl(source);
  host.appendChild(ed.el);
  const out = echoEl();
  host.appendChild(out);
  const act = el('div', 'actions');
  const save = button(zone === 'loaded' ? 'save as pending revision' : 'save → pending', 'primary', async () => {
    save.disabled = true;
    try {
      const r = await post('/api/plugins/save', { name: nameIn.value.trim(), source: ed.value });
      setEcho(out, r.reply);
      if (!name) { name = r.name; nameIn.readOnly = true; }
      zone = 'pending';
      approve.hidden = false;
      refreshCounts();
    } catch (e) {
      setEcho(out, e.message, true);
    } finally {
      save.disabled = false;
    }
  });
  act.appendChild(save);
  const approve = button('approve', null, () => doApprove(false));
  approve.hidden = zone !== 'pending';
  const replace = button('approve with replace', 'danger', () => doApprove(true));
  replace.hidden = true;
  async function doApprove(force) {
    approve.disabled = true;
    replace.disabled = true;
    try {
      const r = await post('/api/plugins/approve', { name: nameIn.value.trim(), replace: force });
      setEcho(out, r.reply);
      replace.hidden = true;
      approve.hidden = true;
      state.pluginZone = 'approved';
      setTimeout(renderPlugins, 900);
    } catch (e) {
      setEcho(out, e.message, true);
      if (e.status === 409) replace.hidden = false;
    } finally {
      approve.disabled = false;
      replace.disabled = false;
    }
  }
  act.appendChild(approve);
  act.appendChild(replace);
  act.appendChild(button('close', null, () => { host.hidden = true; host.innerHTML = ''; }));
  host.appendChild(act);
  async function refreshCounts() {
    try {
      const d = await api('/api/plugins');
      for (const b of main.querySelectorAll('.seg button')) {
        const z = b.textContent.split(' ')[0];
        b.textContent = z + ' ' + (z === 'approved' ? (d.loaded || []).length : (d.pending || []).length);
      }
    } catch (_) {}
  }
  ed.focus();
}

// --- the cwd picker (the server's list + the operator's additions) ---

const ADDED_KEY = 'rig.serve.addedCwds';
const CWD_KEY = 'rig.serve.cwd';

function addedCwds() {
  try { return JSON.parse(localStorage.getItem(ADDED_KEY) || '[]'); } catch (_) { return []; }
}

async function loadCwds() {
  const data = await api('/api/cwds');
  const cwds = data.cwds || [];
  for (const c of addedCwds()) {
    if (!cwds.includes(c)) cwds.push(c);
  }
  cwdSelect.innerHTML = '';
  for (const c of cwds) {
    const o = document.createElement('option');
    o.value = c;
    o.textContent = c;
    cwdSelect.appendChild(o);
  }
  if (cwds.length) {
    let remembered = null;
    try { remembered = localStorage.getItem(CWD_KEY); } catch (_) {}
    state.cwd = remembered && cwds.includes(remembered) ? remembered : cwdSelect.value;
    cwdSelect.value = state.cwd;
  } else {
    state.cwd = '';
  }
}

function addCwd(path) {
  const c = (path !== undefined ? path : cwdAdd.value).trim();
  if (!c) {
    cwdAdd.focus();
    return;
  }
  let found = false;
  for (const o of cwdSelect.options) {
    if (o.value === c) { found = true; break; }
  }
  if (!found) {
    const o = document.createElement('option');
    o.value = c;
    o.textContent = c;
    cwdSelect.appendChild(o);
    const added = addedCwds();
    if (!added.includes(c)) {
      added.push(c);
      try { localStorage.setItem(ADDED_KEY, JSON.stringify(added)); } catch (_) {}
    }
  }
  cwdSelect.value = c;
  cwdAdd.value = '';
  try { localStorage.setItem(CWD_KEY, c); } catch (_) {}
  state.cwd = c;
  state.transcript = null;
  render();
}

// --- the folder browser: ls, for the picker ---

async function browseTo(path) {
  let d;
  try {
    d = await api('/api/fs' + (path ? '?path=' + encodeURIComponent(path) : ''));
  } catch (e) {
    browser.innerHTML = '';
    browser.appendChild(note(G.fail + ' ' + e.message, 'error'));
    return;
  }
  browser.innerHTML = '';
  const head = el('div', 'browse-path');
  head.appendChild(span(G.done + ' ', 'accent'));
  head.appendChild(span('ls', 'accent'));
  head.appendChild(span(' ' + G.dot + ' ', 'dim'));
  head.appendChild(span(d.path, 'text'));
  head.appendChild(button('use this folder', 'primary', () => { addCwd(d.path); setBrowser(false); }));
  if (d.parent) head.appendChild(button('..', null, () => browseTo(d.parent)));
  head.appendChild(button('close', null, () => setBrowser(false)));
  browser.appendChild(head);
  const list = el('div', 'browse-list');
  if (!d.dirs.length) list.appendChild(line('dim')).textContent = '(no folders)';
  for (const e of d.dirs) {
    const row = line('click');
    row.appendChild(span(G.dir + ' ', 'dim'));
    row.appendChild(span(e.name + '/', 'text'));
    row.addEventListener('click', () => browseTo(e.path));
    list.appendChild(row);
  }
  if (d.truncated) list.appendChild(line('dim')).textContent = G.dot + ' more folders not shown (the listing is capped) ' + G.dot;
  browser.appendChild(list);
}

function setBrowser(open) {
  browser.hidden = !open;
  browseBtn.setAttribute('aria-expanded', String(open));
  if (open) browseTo(state.cwd || '');
}

browseBtn.addEventListener('click', () => setBrowser(browser.hidden));

// --- the mobile drawer ---

function setNavOpen(open) {
  document.body.classList.toggle('nav-open', open);
  navToggle.setAttribute('aria-expanded', String(open));
  backdrop.hidden = !open;
}

function isMobile() {
  return window.matchMedia('(max-width: 720px)').matches;
}

navToggle.addEventListener('click', () => {
  setNavOpen(!document.body.classList.contains('nav-open'));
});
backdrop.addEventListener('click', () => setNavOpen(false));

nav.addEventListener('click', (e) => {
  const b = e.target.closest('button[data-view]');
  if (!b) return;
  state.view = b.dataset.view;
  state.transcript = null;
  for (const x of nav.querySelectorAll('button')) x.classList.toggle('active', x === b);
  if (isMobile()) setNavOpen(false);
  render();
});

document.getElementById('cwd-form').addEventListener('submit', (e) => {
  e.preventDefault();
  addCwd();
});

cwdSelect.addEventListener('change', () => {
  state.cwd = cwdSelect.value;
  try { localStorage.setItem(CWD_KEY, state.cwd); } catch (_) {}
  state.transcript = null;
  render();
});

function render() {
  state.transcript = null;
  const needsCwd = state.view === 'sessions' || state.view === 'todos' || state.view === 'scheduler';
  if (needsCwd && !state.cwd) {
    clear();
    main.appendChild(note('pick a workspace (cwd) to read', 'dim'));
    return;
  }
  clear();
  main.appendChild(note('loading…', 'dim'));
  const q = '?cwd=' + encodeURIComponent(state.cwd);
  const p = {
    sessions: () => renderSessions(q),
    todos: () => renderTodos(q),
    scheduler: () => renderScheduler(q),
    models: () => renderModels(),
    plugins: () => renderPlugins(),
  }[state.view];
  p().catch((e) => renderError(e.message));
}

(async function init() {
  try {
    await loadCwds();
  } catch (e) {
    renderError(e.message);
    return;
  }
  render();
})();
