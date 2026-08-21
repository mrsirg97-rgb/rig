// the dashboard's client: the fetch loop, the homage render, the forms.
// Plain JS, no framework, no build step, no external asset. The token rides
// the cookie (set once by the first ?token= load); same-origin fetch carries
// it, so no token handling here.

const nav = document.getElementById('nav');
const main = document.getElementById('main');
const cwdSelect = document.getElementById('cwd');
const cwdAdd = document.getElementById('cwd-add');
const backdrop = document.getElementById('backdrop');
const navToggle = document.getElementById('nav-toggle');

const state = { view: 'sessions', cwd: '', transcript: null };

// the TUI's glyph table (frontend/tui theme.go), unicode set.
const G = {
  pending: '○', active: '◐', done: '●', fail: '✕', ok: '✓',
  compact: '⧉', prompt: '❯', barOn: '▰', barOff: '▱', dot: '·',
};

async function api(path, opts) {
  const res = await fetch(path, opts);
  if (!res.ok) {
    let msg = res.status + ' ' + res.statusText;
    try {
      const j = await res.json();
      if (j && j.error) msg = j.error;
    } catch (_) {}
    throw new Error(msg);
  }
  return res.json();
}

function span(text, cls) {
  const e = document.createElement('span');
  if (cls) e.className = cls;
  e.textContent = text;
  return e;
}

function note(text, cls) {
  const e = document.createElement('p');
  if (cls) e.className = cls;
  e.textContent = text;
  return e;
}

function clear() { main.innerHTML = ''; }

function renderError(msg) {
  clear();
  main.appendChild(note(G.fail + ' ' + msg, 'error'));
}

function verbatimEl(text) {
  const pre = document.createElement('pre');
  pre.className = 'verbatim';
  pre.textContent = text;
  return pre;
}

// the view head: the TUI's tool-block opening line (the ok glyph, the
// accent name, the dim tail).
function viewHead(name, tail) {
  const e = document.createElement('div');
  e.className = 'view-head';
  e.appendChild(span(G.ok + ' ', 'ok'));
  e.appendChild(span(name, 'view-name'));
  if (tail) e.appendChild(span(' ' + G.dot + ' ' + tail, 'dim'));
  return e;
}

// the create forms' reply echo: the store's voice, the arrow line.
function echoEl() {
  const e = document.createElement('pre');
  e.className = 'echo';
  e.hidden = true;
  return e;
}

function setEcho(e, text, isError) {
  e.hidden = false;
  e.textContent = text;
  e.classList.toggle('err', !!isError);
}

function fieldRow(label, input) {
  const d = document.createElement('div');
  d.className = 'field';
  const l = document.createElement('label');
  l.textContent = label;
  d.appendChild(l);
  d.appendChild(input);
  return d;
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
    const line = lines[i];
    if (line === '') continue;
    if (line.startsWith('· ')) {
      if (p.footer) return null;
      p.footer = line;
      continue;
    }
    const tm = line.match(/^  (t\d+) \[([x!~ ])\] (.+)$/);
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

function todoBlockEl(p) {
  const e = document.createElement('div');
  e.className = 'block';
  const bar = progressBar(p.done, p.active, p.total);
  e.appendChild(span(G.barOn.repeat(bar.on), 'bar-on'));
  e.appendChild(span(G.barOff.repeat(bar.off), 'bar-off'));
  let head = ' ' + p.done + '/' + p.total;
  if (p.next) head += ' ' + G.dot + ' next ' + p.next;
  if (p.failed) head += ' ' + G.dot + ' ' + p.failed + ' failed';
  e.appendChild(span(head, 'dim'));
  if (p.action) {
    const a = document.createElement('div');
    a.className = 'action';
    a.appendChild(span('→ ', 'accent'));
    a.appendChild(span(p.action, 'text'));
    e.appendChild(a);
  }
  for (const t of p.tasks) {
    const line = document.createElement('div');
    line.className = 'taskline';
    const [g, cls] = taskGlyph(t.status);
    line.appendChild(span(g + ' ', cls));
    line.appendChild(span(t.id, 'dim'));
    line.appendChild(span(' ' + t.text, 'text'));
    if (t.waits) line.appendChild(span(' ' + G.dot + ' waits on ' + t.waits, 'dim'));
    if (t.claim) line.appendChild(span(' ' + G.dot + ' claimed by ' + t.claim, 'dim'));
    e.appendChild(line);
  }
  if (p.footer) e.appendChild(span('  ' + p.footer, 'dim'));
  return e;
}

// swap the queue area (the block, or the verbatim fallback) in place.
function setQueueArea(text) {
  const area = main.querySelector('.block') || main.querySelector('pre.verbatim');
  if (!area) return;
  const parsed = parseTodo(text);
  area.replaceWith(parsed ? todoBlockEl(parsed) : verbatimEl(text));
}

// --- the TUI's scheduler render, mirrored ---

function parseScheduler(reply) {
  const lines = reply.replace(/\n+$/, '').split('\n');
  if (lines.length === 0) return null;
  if (/^(j\d+) · (\d+) runs? \(oldest first\):$/.test(lines[0])) return { runs: lines };
  const p = { sections: [] };
  let cur = null;
  let job = null;
  for (const line of lines) {
    if (line === '') continue;
    if (line.startsWith('global:') || line.startsWith('cwd:')) {
      job = null;
      cur = {
        name: line.endsWith(': no jobs') ? line.slice(0, -': no jobs'.length) : line.slice(0, -1),
        empty: line.endsWith(': no jobs'),
        jobs: [],
      };
      p.sections.push(cur);
      continue;
    }
    if (!cur) return null;
    if (line.startsWith('  ')) {
      if (!job) return null;
      job.detail.push(line.slice(2));
      continue;
    }
    if (!/^(j\d+)(?: .+)?$/.test(line) || cur.empty) return null;
    job = { head: line, detail: [] };
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

function schedulerBlockEl(p) {
  const e = document.createElement('div');
  e.className = 'block';
  if (p.runs) {
    for (const l of p.runs) e.appendChild(span('  ' + l, 'dim'));
    return e;
  }
  for (const sec of p.sections) {
    const h = document.createElement('div');
    h.className = 'schedsec';
    if (sec.empty) {
      h.appendChild(span('  ' + sec.name + ': no jobs', 'dim'));
    } else {
      h.appendChild(span('  ' + sec.name + ':', 'dim'));
      for (const j of sec.jobs) {
        const row = document.createElement('div');
        row.className = 'schedjob';
        const [g, cls] = schedGlyph(j.head);
        row.appendChild(span(g + ' ', cls));
        row.appendChild(span(j.head, 'text'));
        h.appendChild(row);
        for (const d of j.detail) {
          const dl = document.createElement('div');
          dl.className = d.startsWith('drift: ') ? 'detail drift' : 'detail';
          dl.textContent = '    ' + d;
          h.appendChild(dl);
        }
      }
    }
    e.appendChild(h);
  }
  return e;
}

function setJobsArea(text) {
  const area = main.querySelector('.block');
  if (!area) return;
  const parsed = parseScheduler(text);
  area.replaceWith(parsed ? schedulerBlockEl(parsed) : verbatimEl(text));
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
  main.appendChild(viewHead('sessions', state.cwd));
  const sessions = data.sessions || [];
  if (!sessions.length) {
    main.appendChild(note('no sessions in this workspace', 'dim'));
    return;
  }
  for (const s of sessions) {
    const row = document.createElement('div');
    row.className = 'sess';
    const [g, cls] = exitGlyph(s.exit);
    row.appendChild(span(g + ' ', cls));
    row.appendChild(span(s.id, 'sid'));
    row.appendChild(span('  ' + s.started, 'dim'));
    row.appendChild(span('  ' + s.turns + ' turns', 'dim'));
    row.appendChild(span('  ' + s.exit, 'exit-' + s.exit));
    const b = document.createElement('button');
    b.textContent = 'open';
    b.addEventListener('click', () => renderTranscript(s.id, q));
    row.appendChild(b);
    main.appendChild(row);
  }
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
  }
  return '';
}

function messageEl(m) {
  const block = document.createElement('div');
  block.className = 'msg msg-' + m.role;
  if (m.role === 'user') {
    block.appendChild(span(G.prompt + ' ', 'ember'));
  }
  if (m.reasoning) {
    const r = document.createElement('div');
    r.className = 'reasoning';
    r.textContent = m.reasoning;
    block.appendChild(r);
  }
  if (m.content && m.role !== 'tool') {
    const c = document.createElement('div');
    c.className = 'content';
    c.textContent = m.content;
    block.appendChild(c);
  }
  for (const tc of m.tool_calls || []) {
    const t = document.createElement('div');
    t.className = 'tool';
    const head = document.createElement('div');
    head.className = 'tool-head';
    head.appendChild(span(G.ok + ' ', 'ok'));
    head.appendChild(span(tc.name, 'accent'));
    const d = toolDetail(tc.name, tc.args);
    if (d) {
      head.appendChild(span(' ' + G.dot + ' ', 'dim'));
      head.appendChild(span(d, 'text'));
    }
    const pre = document.createElement('pre');
    pre.textContent = tc.args;
    t.appendChild(head);
    t.appendChild(pre);
    block.appendChild(t);
  }
  if (m.role === 'tool') {
    block.appendChild(span('→ ', 'dim'));
    const pre = document.createElement('pre');
    pre.className = 'toolres';
    pre.textContent = m.content;
    block.appendChild(pre);
  }
  return block;
}

async function renderTranscript(id, q) {
  state.transcript = id;
  const base = '/api/sessions/' + encodeURIComponent(id) + '/transcript';
  const first = await api(base + q);
  clear();
  main.appendChild(viewHead('session ' + id, state.cwd));
  const meta = document.createElement('div');
  const back = document.createElement('button');
  back.textContent = 'back';
  back.addEventListener('click', () => { state.transcript = null; render(); });
  meta.appendChild(back);
  const count = document.createElement('span');
  count.className = 'dim';
  meta.appendChild(count);
  main.appendChild(meta);
  const list = document.createElement('div');
  main.appendChild(list);
  const more = document.createElement('button');
  more.textContent = 'more ↓';
  more.disabled = true;
  main.appendChild(more);
  let offset = 0;
  const paint = (d) => {
    for (const m of d.messages) list.appendChild(messageEl(m));
    offset += d.messages.length;
    count.textContent = ' ' + offset + ' of ' + d.total + ' messages';
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
      main.appendChild(note(G.fail + ' ' + e.message, 'error'));
    }
  });
  const u = usageLine(first.usage);
  if (u) main.appendChild(u);
}

// --- the todos view ---

async function renderTodos(q) {
  const data = await api('/api/todo' + q);
  clear();
  main.appendChild(viewHead('todos', state.cwd));
  const parsed = parseTodo(data.text);
  main.appendChild(parsed ? todoBlockEl(parsed) : verbatimEl(data.text));

  const tog = document.createElement('button');
  tog.className = 'toggle';
  let all = false;
  const label = () => (all ? 'history ' + G.dot + ' ReadAll' : 'actionable ' + G.dot + ' Read');
  tog.textContent = label();
  tog.addEventListener('click', async () => {
    all = !all;
    tog.textContent = G.dot.repeat(3);
    try {
      const d = await api('/api/todo' + q + '&all=' + all);
      setQueueArea(d.text);
    } catch (e) {
      main.appendChild(note(G.fail + ' ' + e.message, 'error'));
    } finally {
      tog.textContent = label();
    }
  });
  main.appendChild(tog);

  const form = document.createElement('form');
  form.className = 'create';
  const ta = document.createElement('textarea');
  ta.rows = 3;
  ta.placeholder = 'one task per line (adds to the queue)';
  const out = echoEl();
  const btn = document.createElement('button');
  btn.type = 'submit';
  btn.textContent = 'create';
  form.appendChild(fieldRow('create (one task per line)', ta));
  form.appendChild(out);
  form.appendChild(btn);
  form.addEventListener('submit', async (ev) => {
    ev.preventDefault();
    btn.disabled = true;
    try {
      const r = await api('/api/todo' + q, {
        method: 'POST',
        headers: { 'Content-Type': 'text/plain' },
        body: ta.value,
      });
      setEcho(out, '→ ' + r.reply);
      ta.value = '';
      const d = await api('/api/todo' + q);
      setQueueArea(d.text);
    } catch (e) {
      setEcho(out, G.fail + ' ' + e.message, true);
    } finally {
      btn.disabled = false;
    }
  });
  main.appendChild(form);
}

// --- the scheduler view ---

async function renderScheduler(q) {
  const data = await api('/api/scheduler' + q);
  clear();
  main.appendChild(viewHead('scheduler', state.cwd));
  const parsed = parseScheduler(data.text);
  main.appendChild(parsed ? schedulerBlockEl(parsed) : verbatimEl(data.text));

  const form = document.createElement('form');
  form.className = 'create';
  const name = document.createElement('input');
  name.type = 'text';
  name.placeholder = 'name (e.g. nightly-digest)';
  name.required = true;
  const prompt = document.createElement('textarea');
  prompt.rows = 2;
  prompt.placeholder = 'the prompt the worker runs';
  prompt.required = true;
  const cron = document.createElement('input');
  cron.type = 'text';
  cron.placeholder = 'cron: 30 7 * * * (five fields, or once)';
  cron.required = true;
  const at = document.createElement('input');
  at.type = 'text';
  at.placeholder = 'at (ISO, only when cron is once): 2026-01-02T03:04:05Z';
  at.hidden = true;
  cron.addEventListener('input', () => { at.hidden = cron.value.trim() !== 'once'; });
  const scope = document.createElement('select');
  scope.appendChild(new Option('this workspace (cwd)'));
  scope.appendChild(new Option('global'));
  const out = echoEl();
  const btn = document.createElement('button');
  btn.type = 'submit';
  btn.textContent = 'create';
  form.appendChild(fieldRow('name', name));
  form.appendChild(fieldRow('prompt', prompt));
  form.appendChild(fieldRow('cron', cron));
  form.appendChild(fieldRow('at', at));
  form.appendChild(fieldRow('scope', scope));
  form.appendChild(out);
  form.appendChild(btn);
  form.addEventListener('submit', async (ev) => {
    ev.preventDefault();
    btn.disabled = true;
    try {
      const body = {
        name: name.value.trim(),
        prompt: prompt.value,
        cron: cron.value.trim(),
      };
      if (body.cron === 'once') body.at = at.value.trim();
      if (scope.selectedIndex === 1) body.scope = 'global';
      const r = await api('/api/scheduler' + q, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      });
      setEcho(out, '→ ' + r.reply);
      name.value = '';
      prompt.value = '';
      cron.value = '';
      at.value = '';
      const d = await api('/api/scheduler' + q);
      setJobsArea(d.text);
    } catch (e) {
      setEcho(out, G.fail + ' ' + e.message, true);
    } finally {
      btn.disabled = false;
    }
  });
  main.appendChild(form);
}

// --- the memory view ---

async function renderMemory(q) {
  const data = await api('/api/memory' + q);
  clear();
  const memories = data.memories || [];
  main.appendChild(viewHead('memory', memories.length + ' recent ' + G.dot + ' ' + state.cwd));
  if (!memmaries.length) {
    main.appendChild(note('no memories for this workspace', 'dim'));
    return;
  }
  const e = document.createElement('div');
  e.className = 'block';
  memories.forEach((m, i) => {
    const line = document.createElement('div');
    line.className = 'memline';
    line.appendChild(span(G.dot + ' ', 'dim'));
    line.appendChild(span(String(i + 1).padStart(2, '0'), 'dim'));
    line.appendChild(span(' ' + m, 'text'));
    e.appendChild(line);
  });
  main.appendChild(e);
}

// --- the models view ---

async function renderModels() {
  const data = await api('/api/models');
  clear();
  main.appendChild(viewHead('models', 'config'));
  const models = data.models || [];
  if (!models.length) {
    main.appendChild(note('no models in the config', 'dim'));
    return;
  }
  const cols = [
    ['id', 'id'], ['window', 'window'], ['max_tokens', 'max tokens'],
    ['reserve', 'reserve'], ['keep_recent', 'keep recent'],
    ['role', 'role'], ['effort', 'effort'],
  ];
  const table = document.createElement('table');
  const head = document.createElement('tr');
  for (const [, label] of cols) {
    const th = document.createElement('th');
    th.textContent = label;
    head.appendChild(th);
  }
  const thE = document.createElement('th');
  thE.textContent = 'efforts';
  head.appendChild(thE);
  table.appendChild(head);
  for (const m of models) {
    const tr = document.createElement('tr');
    for (const [key] of cols) {
      const td = document.createElement('td');
      td.textContent = m[key];
      tr.appendChild(td);
    }
    const td = document.createElement('td');
    td.textContent = (m.efforts || []).join(', ');
    tr.appendChild(td);
    table.appendChild(tr);
  }
  main.appendChild(table);
}

// --- the plugins view ---

async function renderPlugins() {
  const data = await api('/api/plugins');
  clear();
  main.appendChild(viewHead('plugins', 'the home'));
  const section = (title, list) => {
    const h = document.createElement('div');
    h.className = 'plugsec';
    const arr = list || [];
    if (!arr.length) {
      h.appendChild(span('  ' + title + ': no plugins', 'dim'));
    } else {
      h.appendChild(span('  ' + title + ':', 'dim'));
      for (const p of arr) {
        const d = document.createElement('div');
        d.className = 'plugrow';
        d.appendChild(span(G.ok + ' ', 'ok'));
        d.appendChild(span(p.name, 'accent'));
        d.appendChild(span(' ' + G.dot + ' ' + (p.description || '(no DESCRIPTION)'), 'dim'));
        h.appendChild(d);
      }
    }
    main.appendChild(h);
  };
  section('loaded', data.loaded);
  section('pending', data.pending);

  const form = document.createElement('form');
  form.className = 'create';
  const name = document.createElement('input');
  name.type = 'text';
  name.placeholder = 'name (lowercase, the filename stem)';
  name.required = true;
  const desc = document.createElement('input');
  desc.type = 'text';
  desc.placeholder = 'DESCRIPTION (what it does, one line)';
  desc.required = true;
  const code = document.createElement('textarea');
  code.rows = 5;
  code.placeholder = 'the run body: args in, text out, e.g.\n    return "hello " + str(args.get("who") or "world")';
  code.required = true;
  const out = echoEl();
  const btn = document.createElement('button');
  btn.type = 'submit';
  btn.textContent = 'create ' + G.dot + ' lands in plugins/pending';
  form.appendChild(fieldRow('name', name));
  form.appendChild(fieldRow('description', desc));
  form.appendChild(fieldRow('run body (args in, text out)', code));
  form.appendChild(out);
  form.appendChild(btn);
  form.addEventListener('submit', async (ev) => {
    ev.preventDefault();
    btn.disabled = true;
    try {
      const r = await api('/api/plugins', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          name: name.value.trim(),
          description: desc.value.trim(),
          code: code.value,
        }),
      });
      setEcho(out, '→ ' + r.reply);
      name.value = '';
      desc.value = '';
      code.value = '';
      renderPlugins();
    } catch (e) {
      setEcho(out, G.fail + ' ' + e.message, true);
    } finally {
      btn.disabled = false;
    }
  });
  main.appendChild(form);
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
    const remembered = localStorage.getItem(CWD_KEY);
    state.cwd = remembered && cwds.includes(remembered) ? remembered : cwdSelect.value;
    cwdSelect.value = state.cwd;
  } else {
    state.cwd = '';
  }
}

function addCwd() {
  const c = cwdAdd.value.trim();
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
      localStorage.setItem(ADDED_KEY, JSON.stringify(added));
    }
  }
  cwdSelect.value = c;
  cwdAdd.value = '';
  localStorage.setItem(CWD_KEY, c);
  state.cwd = c;
  state.transcript = null;
  render();
}

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
  localStorage.setItem(CWD_KEY, state.cwd);
  state.transcript = null;
  render();
});

function render() {
  state.transcript = null;
  if (!state.cwd) {
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
    memory: () => renderMemory(q),
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
