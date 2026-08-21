// the dashboard's client: the fetch loop, the render, the create form.
// Plain JS, no framework, no build step, no external asset. The token rides
// the cookie (set once by the first ?token= load); same-origin fetch carries
// it, so no token handling here.

const nav = document.getElementById('nav');
const main = document.getElementById('main');
const cwdSelect = document.getElementById('cwd');

const state = { view: 'sessions', cwd: '', transcript: null };

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

function esc(s) {
  return String(s).replace(/[&<>]/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;' }[c]));
}

function note(text, cls) {
  const e = document.createElement('p');
  if (cls) e.className = cls;
  e.textContent = text;
  return e;
}

function h2(text) {
  const e = document.createElement('h2');
  e.textContent = text;
  return e;
}

function clear() { main.innerHTML = ''; }

function renderError(msg) {
  clear();
  main.appendChild(note(msg, 'error'));
}

function verbatim(title, text) {
  clear();
  main.appendChild(h2(title));
  const pre = document.createElement('pre');
  pre.className = 'verbatim';
  pre.textContent = text;
  main.appendChild(pre);
}

async function loadCwds() {
  const data = await api('/api/cwds');
  const cwds = data.cwds || [];
  cwdSelect.innerHTML = '';
  for (const c of cwds) {
    const o = document.createElement('option');
    o.value = c;
    o.textContent = c;
    cwdSelect.appendChild(o);
  }
  if (cwds.length && !state.cwd) state.cwd = cwdSelect.value;
  if (!cwds.length) state.cwd = '';
}

nav.addEventListener('click', (e) => {
  const b = e.target.closest('button[data-view]');
  if (!b) return;
  state.view = b.dataset.view;
  state.transcript = null;
  for (const x of nav.querySelectorAll('button')) x.classList.toggle('active', x === b);
  render();
});

cwdSelect.addEventListener('change', () => {
  state.cwd = cwdSelect.value;
  state.transcript = null;
  render();
});

function render() {
  state.transcript = null;
  if (!state.cwd) {
    renderError('pick a workspace (cwd) to read');
    return;
  }
  main.innerHTML = '';
  main.appendChild(note('loading…', 'dim'));
  const q = '?cwd=' + encodeURIComponent(state.cwd);
  const p = {
    sessions: () => renderSessions(q),
    todos: () => renderTodos(q),
    scheduler: async () => verbatim('Scheduler', (await api('/api/scheduler' + q)).text),
    memory: () => renderMemory(q),
    models: () => renderModels(),
    plugins: () => renderPlugins(),
  }[state.view];
  p().catch((e) => renderError(e.message));
}

async function renderSessions(q) {
  const data = await api('/api/sessions' + q);
  clear();
  main.appendChild(h2('Sessions'));
  const sessions = data.sessions || [];
  if (!sessions.length) {
    main.appendChild(note('no sessions in this workspace', 'dim'));
    return;
  }
  for (const s of sessions) {
    const row = document.createElement('div');
    row.className = 'sess';
    const span = (cls, text) => {
      const e = document.createElement('span');
      e.className = cls;
      e.textContent = text;
      return e;
    };
    row.appendChild(span('sid', s.id));
    row.appendChild(document.createTextNode('  '));
    row.appendChild(span('dim', s.started));
    row.appendChild(document.createTextNode('  '));
    row.appendChild(span('exit-' + s.exit, s.exit));
    row.appendChild(document.createTextNode('  '));
    row.appendChild(span('dim', s.turns + ' turns'));
    const b = document.createElement('button');
    b.textContent = 'open';
    b.addEventListener('click', () => renderTranscript(s.id, q));
    row.appendChild(document.createTextNode(' '));
    row.appendChild(b);
    main.appendChild(row);
  }
}

async function renderTranscript(id, q) {
  state.transcript = id;
  const data = await api('/api/sessions/' + encodeURIComponent(id) + '/transcript' + q);
  clear();
  main.appendChild(h2('Session ' + id));
  const back = document.createElement('button');
  back.textContent = 'back';
  back.addEventListener('click', () => { state.transcript = null; render(); });
  main.appendChild(back);
  main.appendChild(document.createTextNode('  ' + (data.has_more ? '· ' + data.offset + '–' + (data.offset + data.messages.length) + ' of ' + data.total : '· ' + data.total + ' messages')));

  for (const m of data.messages) {
    const block = document.createElement('div');
    block.className = 'msg msg-' + m.role;
    if (m.reasoning) {
      const r = document.createElement('div');
      r.className = 'reasoning';
      r.textContent = m.reasoning;
      block.appendChild(r);
    }
    if (m.content) {
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
      head.textContent = tc.name + '  ' + tc.id;
      const pre = document.createElement('pre');
      pre.textContent = tc.args;
      t.appendChild(head);
      t.appendChild(pre);
      block.appendChild(t);
    }
    if (m.tool_id) {
      const t = document.createElement('div');
      t.className = 'toolres';
      const head = document.createElement('div');
      head.className = 'dim';
      head.textContent = 'tool result ' + m.tool_id;
      const pre = document.createElement('pre');
      pre.textContent = m.content;
      t.appendChild(head);
      t.appendChild(pre);
      block.appendChild(t);
    }
    main.appendChild(block);
  }

  if (data.usage && data.usage.length) {
    const u = document.createElement('div');
    u.className = 'usage';
    const dim = document.createElement('div');
    dim.className = 'dim';
    dim.textContent = 'usage (per turn)';
    const pre = document.createElement('pre');
    pre.textContent = data.usage
      .map((x) => '#' + x.seq + '  prompt ' + x.prompt + '  completion ' + x.completion + '  cache_read ' + x.cache_read + '  cache_write ' + x.cache_write)
      .join('\n');
    u.appendChild(dim);
    u.appendChild(pre);
    main.appendChild(u);
  }
}

async function renderTodos(q) {
  const data = await api('/api/todo' + q);
  clear();
  main.appendChild(h2('Todos'));
  const pre = document.createElement('pre');
  pre.className = 'verbatim';
  pre.textContent = data.text;
  main.appendChild(pre);

  const tog = document.createElement('button');
  let all = false;
  const label = () => (all ? 'show actionable (Read)' : 'show history (ReadAll)');
  tog.textContent = label();
  tog.addEventListener('click', async () => {
    all = !all;
    tog.textContent = '…';
    const d = await api('/api/todo' + q + '&all=' + all);
    pre.textContent = d.text;
    tog.textContent = label();
  });
  main.appendChild(tog);

  main.appendChild(document.createTextNode(' '));
  const form = document.createElement('div');
  form.className = 'create';
  const ta = document.createElement('textarea');
  ta.rows = 3;
  ta.placeholder = 'one task per line (adds to the queue)';
  const btn = document.createElement('button');
  btn.textContent = 'create';
  const out = document.createElement('pre');
  out.className = 'create-out';
  out.hidden = true;
  form.appendChild(ta);
  form.appendChild(document.createTextNode(' '));
  form.appendChild(btn);
  form.appendChild(out);
  btn.addEventListener('click', async () => {
    out.hidden = false;
    out.textContent = '…';
    try {
      const r = await api('/api/todo' + q, {
        method: 'POST',
        headers: { 'Content-Type': 'text/plain' },
        body: ta.value,
      });
      out.textContent = r.reply;
      ta.value = '';
      const d = await api('/api/todo' + q);
      pre.textContent = d.text;
    } catch (e) {
      out.textContent = 'error: ' + e.message;
    }
  });
  main.appendChild(form);
}

async function renderMemory(q) {
  const data = await api('/api/memory' + q);
  clear();
  main.appendChild(h2('Memory'));
  const memories = data.memories || [];
  if (!memories.length) {
    main.appendChild(note('no memories for this cwd', 'dim'));
    return;
  }
  for (const m of memories) {
    const d = document.createElement('div');
    d.className = 'mem';
    d.textContent = m;
    main.appendChild(d);
  }
}

async function renderModels() {
  const data = await api('/api/models');
  clear();
  main.appendChild(h2('Models'));
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

async function renderPlugins() {
  const data = await api('/api/plugins');
  clear();
  main.appendChild(h2('Plugins'));
  const section = (title, list) => {
    const h = document.createElement('h3');
    h.textContent = title + ' (' + (list || []).length + ')';
    main.appendChild(h);
    if (!list || !list.length) {
      main.appendChild(note('none', 'dim'));
      return;
    }
    for (const p of list) {
      const d = document.createElement('div');
      d.className = 'plug';
      const name = document.createElement('div');
      name.className = 'plug-name';
      name.textContent = p.name;
      const desc = document.createElement('div');
      desc.className = 'dim';
      desc.textContent = p.description || '(no DESCRIPTION)';
      d.appendChild(name);
      d.appendChild(desc);
      main.appendChild(d);
    }
  };
  section('loaded', data.loaded);
  section('pending', data.pending);
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
