'use strict';

/* Drip patch panel. A tunnel is a port: allocated or blank, lit or dark. */

// ---- transport -----------------------------------------------------------

function csrfToken() {
  const match = document.cookie.match(/(?:^|;\s*)drip_admin_csrf=([^;]*)/);
  return match ? decodeURIComponent(match[1]) : '';
}

async function api(method, path, body) {
  const opts = { method, headers: { Accept: 'application/json' }, credentials: 'same-origin' };
  if (body !== undefined) {
    opts.headers['Content-Type'] = 'application/json';
    opts.body = JSON.stringify(body);
  }
  // Every state-changing verb echoes the CSRF cookie into a header. An
  // off-origin page cannot read that cookie, so it cannot forge the header.
  if (method !== 'GET') opts.headers['X-CSRF-Token'] = csrfToken();

  const res = await fetch(path, opts);
  const text = await res.text();
  let data = null;
  if (text) { try { data = JSON.parse(text); } catch (_) { data = null; } }

  if (!res.ok) {
    const err = new Error((data && data.error) || res.statusText || 'request failed');
    err.status = res.status;
    throw err;
  }
  return data;
}

// ---- dom -----------------------------------------------------------------

function el(tag, attrs, children) {
  const node = document.createElement(tag);
  if (attrs) {
    for (const [k, v] of Object.entries(attrs)) {
      if (v === null || v === undefined || v === false) continue;
      if (k === 'class') node.className = v;
      else if (k === 'text') node.textContent = v;
      else if (k.startsWith('on')) node.addEventListener(k.slice(2), v);
      else node.setAttribute(k, v === true ? '' : v);
    }
  }
  for (const child of children || []) if (child) node.appendChild(child);
  return node;
}

function svgIcon(id) {
  const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
  const use = document.createElementNS('http://www.w3.org/2000/svg', 'use');
  use.setAttribute('href', '#' + id);
  svg.setAttribute('aria-hidden', 'true');
  svg.appendChild(use);
  return svg;
}

let statusTimer = null;
function status(message, bad) {
  const node = document.getElementById('status');
  node.textContent = message;
  node.classList.toggle('bad', !!bad);
  node.hidden = false;
  clearTimeout(statusTimer);
  statusTimer = setTimeout(() => { node.hidden = true; }, bad ? 9000 : 4500);
}

function bytes(n) {
  if (!n) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let i = 0, v = n;
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i++; }
  return `${v.toFixed(i ? 1 : 0)} ${units[i]}`;
}

function when(value) {
  if (!value) return 'never';
  const d = new Date(value);
  return Number.isNaN(d.getTime()) ? 'never' : d.toLocaleString();
}

function pad(n) { return String(n).padStart(2, '0'); }

// ---- state ---------------------------------------------------------------

const state = {
  user: null,
  view: 'field',
  server: null,
  accounts: [],
  clients: [],
  reservations: [],
  linked: new Set(),   // subdomains lit at last render, for the energize moment
  seenLink: false,
};

function accountOf(id) { return state.accounts.find(a => a.id === id) || null; }
function accountLabel(id) { const a = accountOf(id); return a ? a.name : (id || '—'); }
function clientLabel(id) {
  if (!id) return 'any client on the account';
  const c = state.clients.find(x => x.id === id);
  return c ? c.name : id;
}
function tagClass(accountId) {
  const index = state.accounts.findIndex(a => a.id === accountId);
  return 'tag tag-' + (index < 0 ? 5 : (index % 8) + 1);
}
function canEdit() { return state.user && state.user.role === 'admin'; }

// multiTenant reports whether account columns and tags distinguish anything.
function multiTenant() { return state.accounts.length > 1; }

// cols drops the entries whose `when` is false, keeping headers and cells aligned.
function cols(entries) { return entries.filter(e => e.when !== false).map(e => e.value); }

async function loadLookups() {
  const [server, accounts, clients, reservations] = await Promise.all([
    state.server ? Promise.resolve(state.server) : api('GET', '/api/server'),
    api('GET', '/api/accounts'),
    api('GET', '/api/clients'),
    api('GET', '/api/reservations'),
  ]);
  state.server = server || null;
  state.accounts = accounts || [];
  state.clients = clients || [];
  state.reservations = reservations || [];
}

// ---- what a machine actually needs -------------------------------------

// tunnelURL mirrors the server's own URL builder: the port is omitted at 443.
function tunnelURL(subdomain) {
  const s = state.server;
  if (!s || !s.tunnel_domain || !subdomain) return '';
  const host = `${subdomain}.${s.tunnel_domain}`;
  return s.public_port === 443 ? `https://${host}` : `https://${host}:${s.public_port}`;
}

function tcpURL(port) {
  const s = state.server;
  if (!s || !s.tunnel_domain || !port) return '';
  return `tcp://${s.tunnel_domain}:${port}`;
}

// allocationURL is the address an allocation resolves to once a machine binds it.
function allocationURL(reservation) {
  return reservation.subdomain
    ? tunnelURL(reservation.subdomain)
    : tcpURL(reservation.tcp_port);
}

// connectCommand is the line an operator pastes on the machine being connected.
// The token only exists at issue time, so elsewhere it stands in as a placeholder.
function connectCommand(token, localPort) {
  const s = state.server;
  const server = s && s.domain
    ? `${s.domain}:${s.public_port || 443}`
    : 'your-server:443';
  return `drip http ${localPort || 8080} --server ${server} --token ${token}`;
}

// reservationFor finds the allocation a credential is bound to, if any.
function reservationFor(clientId) {
  return (state.reservations || []).find(r => r.client_id === clientId) || null;
}

// copyable renders a monospace strip with a copy button beside it.
function copyable(value, label) {
  const button = el('button', { type: 'button', class: 'btn-quiet small', text: 'Copy' });
  button.addEventListener('click', async () => {
    try {
      await navigator.clipboard.writeText(value);
      button.textContent = 'Copied';
      setTimeout(() => { button.textContent = 'Copy'; }, 1600);
    } catch (_) {
      status('Could not reach the clipboard. Select the text and copy it manually.', true);
    }
  });
  return el('div', { class: 'copyable' }, [
    label ? el('span', { class: 'legend', text: label }) : null,
    el('div', { class: 'copyable-row' }, [
      el('code', { class: 'tag-strip', text: value }),
      button,
    ]),
  ]);
}

// ---- shared pieces -------------------------------------------------------

function head(title, note) {
  return el('div', { class: 'view-head' }, [
    el('h1', { text: title }),
    el('p', { class: 'note', text: note }),
  ]);
}

function panel(railChildren, bodyChildren) {
  const parts = [];
  if (railChildren) parts.push(el('div', { class: 'panel-rail' }, railChildren));
  if (bodyChildren) parts.push(el('div', { class: 'panel-body' }, bodyChildren));
  return el('div', { class: 'panel' }, parts);
}

function dataTable(headers, rows, emptyTitle, emptyHint) {
  if (!rows.length) {
    return el('div', { class: 'blank' }, [
      el('span', { class: 'legend', text: emptyTitle }),
      el('p', { class: 'note', text: emptyHint }),
    ]);
  }
  return el('div', { class: 'scroll' }, [
    el('table', null, [
      el('thead', null, [el('tr', null, headers.map(h => {
        const spec = typeof h === 'string' ? { label: h } : h;
        return el('th', { class: spec.right ? 'right' : null, text: spec.label || '' });
      }))]),
      el('tbody', null, rows),
    ]),
  ]);
}

function stateCell(kind, label) {
  return el('span', { class: 'state ' + kind }, [
    el('i', { class: 'led' }),
    el('span', { text: label }),
  ]);
}

// A destructive action swaps itself for an inline confirmation. No modal, and
// no browser dialog that would block the whole session.
function danger(label, confirmText, run) {
  const button = el('button', { type: 'button', class: 'btn-danger small', text: label });

  button.addEventListener('click', () => {
    // Replace the whole action group, not just this button: a confirmation
    // squeezed in beside the others overflows the column.
    const group = button.closest('.btn-row') || button;
    const cancel = el('button', { type: 'button', class: 'btn-quiet small', text: 'Keep' });
    const go = el('button', { type: 'button', class: 'btn-danger small', text: label });
    const strip = el('span', { class: 'confirm' }, [
      el('span', { text: confirmText }), cancel, go,
    ]);

    const restore = () => { strip.replaceWith(group); button.focus(); };
    cancel.addEventListener('click', restore);
    go.addEventListener('click', async () => {
      go.disabled = true;
      cancel.disabled = true;
      go.textContent = 'Working';
      try { await run(); } catch (err) { status(err.message, true); restore(); }
    });

    group.replaceWith(strip);
    cancel.focus();
  });

  return button;
}

// accountField returns the account control for a form, or null when there is
// nothing to decide. With a single account the id rides along as a hidden
// input, so a one-tenant deployment never sees the concept at all.
function accountField(name) {
  if (state.accounts.length === 1) {
    return el('input', { type: 'hidden', name, value: state.accounts[0].id });
  }
  const sel = el('select', { name });
  for (const a of state.accounts) sel.appendChild(el('option', { value: a.id, text: a.name }));
  return labelled('Account', sel);
}

// hasAccount reports whether any account exists to attach things to.
function hasAccount() { return state.accounts.length > 0; }

// needsAccountFirst is the blank state shown when nothing can be created yet.
function needsAccountFirst(what) {
  return el('div', { class: 'blank' }, [
    el('span', { class: 'legend', text: 'No account yet' }),
    el('p', { class: 'note', text: `Every ${what} belongs to an account. Add one under Accounts first.` }),
  ]);
}

function clientSelect(name) {
  const sel = el('select', { name });
  sel.appendChild(el('option', { value: '', text: 'any machine on the account' }));
  for (const c of state.clients) sel.appendChild(el('option', { value: c.id, text: c.name }));
  return sel;
}

function submitting(form, on) {
  for (const control of form.elements) control.disabled = on;
  const button = form.querySelector('button[type=submit]');
  if (button) button.textContent = on ? 'Working' : button.dataset.label;
}

function bench(fields, submitLabel, handler) {
  const button = el('button', { type: 'submit', class: 'btn', text: submitLabel });
  button.dataset.label = submitLabel;
  const form = el('form', { class: 'bench' }, [...fields, button]);

  form.addEventListener('submit', async (e) => {
    e.preventDefault();
    // Read the values before disabling anything: FormData skips disabled
    // controls, so locking the form first would submit an empty body.
    const data = new FormData(form);
    submitting(form, true);
    try {
      await handler(data);
      form.reset();
      await render();
    } catch (err) {
      status(err.message, true);
      submitting(form, false);
    }
  });
  return form;
}

function labelled(text, control, grow) {
  return el('label', { class: 'field' + (grow ? ' grow' : '') }, [
    el('span', { class: 'legend', text }), control,
  ]);
}

// ---- field ---------------------------------------------------------------

function portKey(r) { return r.subdomain || ('tcp-' + r.tcp_port); }

async function viewField(root) {
  const reservations = state.reservations;
  const tunnels = await api('GET', '/api/tunnels');

  const live = new Map();
  for (const t of tunnels || []) live.set(t.subdomain, t);

  // Every allocation is a port. A live tunnel nobody allocated is an
  // unlabelled patch, and gets a port at the end of the field.
  const ports = (reservations || []).map(r => ({
    key: portKey(r),
    label: r.subdomain || `tcp ${r.tcp_port}`,
    accountId: r.account_id,
    clientId: r.client_id,
    enabled: r.enabled,
    tunnel: live.get(portKey(r)) || null,
    allocated: true,
  }));

  for (const t of tunnels || []) {
    if (!ports.some(p => p.key === t.subdomain)) {
      ports.push({
        key: t.subdomain,
        label: t.subdomain,
        accountId: t.account_id,
        clientId: t.client_id,
        enabled: true,
        tunnel: t,
        allocated: false,
      });
    }
  }

  const linkedNow = new Set(ports.filter(p => p.tunnel).map(p => p.key));
  const linkedCount = linkedNow.size;
  const darkCount = ports.filter(p => p.allocated && !p.tunnel).length;

  root.appendChild(head(
    'Field',
    'Every allocation is a port. A lit indicator means a client is connected to it right now.',
  ));

  const rail = [
    el('div', { class: 'counts' }, [
      el('span', { class: 'count linked' }, [
        el('b', { text: String(linkedCount) }), el('span', { class: 'legend', text: 'linked' }),
      ]),
      el('span', { class: 'count' }, [
        el('b', { text: String(darkCount) }), el('span', { class: 'legend', text: 'allocated, dark' }),
      ]),
      el('span', { class: 'count' }, [
        el('b', { text: String(ports.length) }), el('span', { class: 'legend', text: 'ports' }),
      ]),
    ]),
    el('button', {
      type: 'button', class: 'btn-quiet small', text: 'Re-read',
      onclick: () => render(),
    }),
  ];

  if (!ports.length) {
    root.appendChild(panel(rail, [
      el('div', { class: 'blank' }, [
        el('span', { class: 'legend', text: 'Panel empty' }),
        el('p', {
          class: 'note',
          text: 'No allocations and nothing connected. Create an account, issue a credential, then reserve a name for it — the reserved name is what the client binds to on every reconnect.',
        }),
      ]),
    ]));
    return;
  }

  const grid = el('div', { class: 'field-grid' });
  ports.forEach((p, i) => {
    const linked = !!p.tunnel;
    const fresh = linked && state.seenLink && !state.linked.has(p.key);

    let cls = 'port';
    if (linked) cls += ' is-linked';
    if (fresh) cls += ' just-linked';
    if (!p.enabled) cls += ' is-disabled is-held';

    const stripClass = p.allocated ? 'port-strip' : 'port-strip unlabelled';
    const detail = linked
      ? `${p.tunnel.active_connections} conn · ${bytes(p.tunnel.bytes_in + p.tunnel.bytes_out)}`
      : (p.allocated ? (p.enabled ? 'dark' : 'disabled') : 'unlabelled');

    grid.appendChild(el('div', {
      class: cls,
      title: `${p.label} — ${accountLabel(p.accountId)}`,
    }, [
      el('span', { class: 'port-no', text: pad(i + 1) }),
      el('span', { class: 'port-jack' }, [svgIcon('keystone'), el('i', { class: 'led' })]),
      el('span', { class: stripClass, text: p.label }),
      el('span', { class: 'port-meta' }, [
        el('i', { class: tagClass(p.accountId) }),
        el('span', { class: 'legend', text: detail }),
      ]),
    ]));
  });

  state.linked = linkedNow;
  state.seenLink = true;

  // A rack panel is never left half-populated; unused positions carry blanking
  // plates. Recomputed on reflow so the row stays full at every width.
  const fillBlanks = () => {
    for (const blank of grid.querySelectorAll('.port-blank')) blank.remove();
    const columns = getComputedStyle(grid).gridTemplateColumns.split(' ').filter(Boolean).length;
    if (!columns) return;
    const remainder = ports.length % columns;
    if (!remainder) return;
    for (let i = remainder; i < columns; i++) {
      grid.appendChild(el('div', { class: 'port port-blank', 'aria-hidden': 'true' }));
    }
  };

  root.appendChild(panel(rail, [grid]));
  fillBlanks();
  if (window.ResizeObserver) {
    const observer = new ResizeObserver(fillBlanks);
    observer.observe(grid);
  }

  // What is actually flowing. Only linked ports appear here; the field above
  // already answers "is it up", this answers "what is it doing".
  const linkedPorts = ports.filter(p => p.tunnel);
  if (linkedPorts.length) {
    const rows = linkedPorts.map(p => el('tr', null, cols([
      { value: el('td', null, [el('span', { class: 'state' }, [
        el('i', { class: tagClass(p.accountId) }),
        el('span', { class: 'mono', text: p.label }),
      ])]) },
      { value: el('td', { text: p.tunnel.tunnel_type }) },
      { value: el('td', { text: p.clientId ? clientLabel(p.clientId) : 'unauthenticated' }) },
      { value: el('td', { text: accountLabel(p.accountId) }), when: multiTenant() },
      { value: el('td', { class: 'right', text: String(p.tunnel.active_connections) }) },
      { value: el('td', { class: 'right', text: bytes(p.tunnel.bytes_in) }) },
      { value: el('td', { class: 'right', text: bytes(p.tunnel.bytes_out) }) },
      { value: el('td', { text: when(p.tunnel.last_active) }) },
    ])));

    root.appendChild(panel(
      [el('span', { class: 'legend', text: 'Linked detail' })],
      [dataTable(
        cols([
          { value: 'Port' }, { value: 'Type' }, { value: 'Machine' },
          { value: 'Account', when: multiTenant() },
          { value: { label: 'Conns', right: true } },
          { value: { label: 'In', right: true } },
          { value: { label: 'Out', right: true } },
          { value: 'Last active' },
        ]),
        rows, '', '',
      )],
    ));
  }

  // Which colour tags which account, printed on the panel like a legend card.
  if (multiTenant()) {
    root.appendChild(panel(
      [
        el('span', { class: 'legend', text: 'Tag legend' }),
        el('div', { class: 'counts legend-row' }, state.accounts.map(a =>
          el('span', { class: 'count' }, [
            el('i', { class: tagClass(a.id) }),
            el('span', { class: 'legend', text: a.name }),
          ]))),
      ],
      null,
    ));
  }
}

// ---- allocations ---------------------------------------------------------

async function viewReservations(root) {
  const list = state.reservations;

  root.appendChild(head(
    'Allocations',
    'A reserved subdomain or TCP port. Bind it to a machine and that machine lands on the same URL every time it reconnects; leave it unbound and any machine may claim it by asking for the name.',
  ));

  if (canEdit() && !hasAccount()) {
    root.appendChild(panel(null, [needsAccountFirst('allocation')]));
  } else if (canEdit()) {
    root.appendChild(panel(
      [el('span', { class: 'legend', text: 'Reserve a port' })],
      [bench([
        accountField('account_id'),
        labelled('Machine', clientSelect('client_id')),
        labelled('Subdomain', el('input', { name: 'subdomain', placeholder: 'billing', autocapitalize: 'off', spellcheck: 'false' }), true),
        labelled('or TCP port', el('input', { name: 'tcp_port', type: 'number', min: '1', max: '65535', placeholder: '20050' })),
        labelled('Bandwidth', el('input', { name: 'bandwidth', placeholder: '1M' })),
      ], 'Reserve', async (data) => {
        const port = parseInt(data.get('tcp_port'), 10);
        await api('POST', '/api/reservations', {
          account_id: data.get('account_id'),
          client_id: data.get('client_id') || null,
          subdomain: (data.get('subdomain') || '').trim(),
          tcp_port: Number.isNaN(port) ? 0 : port,
          bandwidth: (data.get('bandwidth') || '').trim(),
        });
        status('Port reserved.');
      })],
    ));
  }

  const rows = list.map(r => el('tr', null, cols([
    { value: el('td', null, [el('span', { class: 'state' }, [
      el('i', { class: tagClass(r.account_id) }),
      el('span', { class: 'mono', text: allocationURL(r) || r.subdomain || `tcp ${r.tcp_port}` }),
    ])]) },
    { value: el('td', { text: r.tunnel_type }) },
    { value: el('td', { text: accountLabel(r.account_id) }), when: multiTenant() },
    { value: el('td', { text: clientLabel(r.client_id) }) },
    { value: el('td', { text: r.bandwidth || '—' }) },
    { value: el('td', null, [r.enabled ? stateCell('on', 'enabled') : stateCell('held', 'disabled')]) },
    { value: el('td', { class: 'right' }, [canEdit() ? el('span', { class: 'btn-row' }, [
      el('button', {
        type: 'button', class: 'btn-quiet small', text: r.enabled ? 'Disable' : 'Enable',
        onclick: async (e) => {
          e.target.disabled = true;
          try {
            await api('PATCH', `/api/reservations/${r.id}`, { enabled: !r.enabled });
            await render();
          } catch (err) { status(err.message, true); e.target.disabled = false; }
        },
      }),
      danger('Release', 'Release this name?', async () => {
        await api('DELETE', `/api/reservations/${r.id}`);
        status('Allocation released.');
        await render();
      }),
    ]) : null]) },
  ])));

  root.appendChild(panel(
    [el('span', { class: 'legend', text: `${list.length} allocated` })],
    [dataTable(
      cols([
        { value: 'Port' }, { value: 'Type' },
        { value: 'Account', when: multiTenant() },
        { value: 'Machine' }, { value: 'Bandwidth' }, { value: 'State' },
        { value: { label: '', right: true } },
      ]),
      rows,
      'Nothing allocated',
      'Reserve a subdomain and every reconnect from that client lands on the same URL.',
    )],
  ));
}

// ---- credentials ---------------------------------------------------------

async function viewClients(root) {
  root.appendChild(head(
    'Credentials',
    'One credential per machine. The machine presents it to connect; the token is shown once, when it is issued or rotated.',
  ));

  if (canEdit() && !hasAccount()) {
    root.appendChild(panel(null, [needsAccountFirst('credential')]));
  } else if (canEdit()) {
    root.appendChild(panel(
      [el('span', { class: 'legend', text: 'Issue a credential' })],
      [bench([
        accountField('account_id'),
        labelled('Machine name', el('input', { name: 'name', placeholder: 'win-svc-01', required: true, autocapitalize: 'off', spellcheck: 'false' }), true),
        labelled('Bandwidth', el('input', { name: 'bandwidth', placeholder: '1M' })),
      ], 'Issue', async (data) => {
        const created = await api('POST', '/api/clients', {
          account_id: data.get('account_id'),
          name: (data.get('name') || '').trim(),
          bandwidth: (data.get('bandwidth') || '').trim(),
        });
        showToken(created.token, created);
      })],
    ));
  }

  const rows = state.clients.map(c => el('tr', null, cols([
    { value: el('td', null, [el('span', { class: 'state' }, [
      el('i', { class: tagClass(c.account_id) }),
      el('span', { text: c.name }),
    ])]) },
    { value: el('td', null, [(() => {
      const reservation = reservationFor(c.id);
      return reservation
        ? el('span', { class: 'mono', text: allocationURL(reservation) })
        : el('span', { class: 'legend', text: 'random name each connect' });
    })()]) },
    { value: el('td', { text: accountLabel(c.account_id) }), when: multiTenant() },
    { value: el('td', { text: c.bandwidth || '—' }) },
    { value: el('td', { text: when(c.last_seen_at) }) },
    { value: el('td', null, [c.enabled ? stateCell('on', 'enabled') : stateCell('held', 'disabled')]) },
    { value: el('td', { class: 'right' }, [canEdit() ? el('span', { class: 'btn-row' }, [
      el('button', {
        type: 'button', class: 'btn-quiet small', text: c.enabled ? 'Disable' : 'Enable',
        onclick: async (e) => {
          e.target.disabled = true;
          try {
            await api('PATCH', `/api/clients/${c.id}`, { enabled: !c.enabled });
            await render();
          } catch (err) { status(err.message, true); e.target.disabled = false; }
        },
      }),
      danger('Rotate', 'Rotate? The old token dies now.', async () => {
        const out = await api('POST', `/api/clients/${c.id}/rotate`);
        showToken(out.token, c);
        await render();
      }),
      danger('Delete', 'Delete? Allocations become unbound.', async () => {
        await api('DELETE', `/api/clients/${c.id}`);
        status('Credential deleted.');
        await render();
      }),
    ]) : null]) },
  ])));

  root.appendChild(panel(
    [el('span', { class: 'legend', text: `${state.clients.length} issued` })],
    [dataTable(
      cols([
        { value: 'Machine' }, { value: 'Address' },
        { value: 'Account', when: multiTenant() },
        { value: 'Bandwidth' }, { value: 'Last seen' }, { value: 'State' },
        { value: { label: '', right: true } },
      ]),
      rows,
      'No credentials issued',
      'A credential is what a machine presents to connect. Issue one per machine, then reserve a port for it.',
    )],
  ));
}

// ---- accounts ------------------------------------------------------------

async function viewAccounts(root) {
  root.appendChild(head(
    'Accounts',
    'An account groups the machines and names belonging to one tenant, and caps how many tunnels they may open at once. Running this server for yourself? Keep the single default account and ignore this page — you only need more when separate customers or teams share the deployment.',
  ));

  if (canEdit()) {
    root.appendChild(panel(
      [el('span', { class: 'legend', text: 'Add an account' })],
      [bench([
        labelled('Name', el('input', { name: 'name', placeholder: 'acme', required: true, autocapitalize: 'off' }), true),
        labelled('Tunnel ceiling (0 = none)', el('input', { name: 'max_tunnels', type: 'number', min: '0', value: '0' })),
      ], 'Add', async (data) => {
        await api('POST', '/api/accounts', {
          name: (data.get('name') || '').trim(),
          max_tunnels: parseInt(data.get('max_tunnels'), 10) || 0,
        });
        status('Account added.');
      })],
    ));
  }

  const rows = state.accounts.map(a => el('tr', null, [
    el('td', null, [el('span', { class: 'state' }, [
      el('i', { class: tagClass(a.id) }), el('span', { text: a.name }),
    ])]),
    el('td', { class: 'mono', text: a.id }),
    el('td', { text: a.max_tunnels ? String(a.max_tunnels) : 'no ceiling' }),
    el('td', null, [a.enabled ? stateCell('on', 'enabled') : stateCell('held', 'disabled')]),
    el('td', { class: 'right' }, [canEdit() ? el('span', { class: 'btn-row' }, [
      el('button', {
        type: 'button', class: 'btn-quiet small', text: a.enabled ? 'Disable' : 'Enable',
        onclick: async (e) => {
          e.target.disabled = true;
          try {
            await api('PATCH', `/api/accounts/${a.id}`, { enabled: !a.enabled });
            await render();
          } catch (err) { status(err.message, true); e.target.disabled = false; }
        },
      }),
      danger('Delete', 'Delete with all its credentials and allocations?', async () => {
        await api('DELETE', `/api/accounts/${a.id}`);
        status('Account deleted.');
        await render();
      }),
    ]) : null]),
  ]));

  root.appendChild(panel(
    [el('span', { class: 'legend', text: `${state.accounts.length} accounts` })],
    [dataTable(
      ['Account', 'ID', 'Tunnel ceiling', 'State', { label: '', right: true }],
      rows,
      'No accounts',
      'An account is the owner every credential and allocation hangs off. Add one to begin.',
    )],
  ));
}

// ---- log -----------------------------------------------------------------

async function viewAudit(root) {
  const entries = await api('GET', '/api/audit?limit=200') || [];

  root.appendChild(head('Log', 'Administrative changes, most recent first.'));

  const rows = entries.map(e => el('tr', null, [
    el('td', { text: when(e.at) }),
    el('td', { text: e.actor_id || e.actor_type }),
    el('td', { class: 'mono', text: e.action }),
    el('td', { class: 'wrap', text: e.detail || e.target_id || '—' }),
    el('td', { class: 'mono', text: e.ip || '—' }),
  ]));

  root.appendChild(panel(
    [el('span', { class: 'legend', text: `${entries.length} entries` })],
    [dataTable(
      ['When', 'Who', 'Action', 'Target', 'From'],
      rows,
      'Nothing logged yet',
      'Every change made through this panel is recorded here with who made it and from where.',
    )],
  ));
}

// ---- token reveal --------------------------------------------------------

const tokenDialog = document.getElementById('token-dialog');

function showToken(token, client) {
  const body = document.getElementById('token-body');
  body.textContent = '';

  const reservation = client ? reservationFor(client.id) : null;
  const url = reservation ? allocationURL(reservation) : '';

  body.appendChild(copyable(connectCommand(token, 8080), 'Run this on the machine'));
  body.appendChild(copyable(token, 'Token on its own'));

  if (url) {
    body.appendChild(el('p', { class: 'note' }, [
      document.createTextNode('Once connected, this machine is reachable at '),
      el('strong', { text: url }),
      document.createTextNode('. It binds the same address on every reconnect.'),
    ]));
  } else {
    body.appendChild(el('p', {
      class: 'note',
      text: 'This machine has no reserved name yet, so it will get a random one each time it connects. Reserve a port for it under Allocations to pin it.',
    }));
  }

  tokenDialog.returnValue = '';
  tokenDialog.showModal();
}

document.getElementById('token-done').addEventListener('click', () => {
  tokenDialog.close();
  document.getElementById('token-body').textContent = '';
});

// The token cannot be recovered, so dismissing by Escape must be deliberate too.
tokenDialog.addEventListener('cancel', (e) => {
  e.preventDefault();
  status('Copy the token before closing — it is not shown again.', true);
});

// ---- shell ---------------------------------------------------------------

const views = {
  field: viewField,
  reservations: viewReservations,
  clients: viewClients,
  accounts: viewAccounts,
  audit: viewAudit,
};

function skeleton(root) {
  const grid = el('div', { class: 'field-grid' });
  for (let i = 0; i < 8; i++) {
    grid.appendChild(el('div', { class: 'port skeleton-port' }, [
      el('div', { class: 'skeleton', style: 'width:40%' }),
      el('div', { class: 'skeleton', style: 'width:80%' }),
      el('div', { class: 'skeleton', style: 'width:60%' }),
    ]));
  }
  root.appendChild(el('div', { class: 'panel' }, [el('div', { class: 'panel-body' }, [grid])]));
}

let rendering = false;
async function render() {
  if (rendering) return;
  rendering = true;

  const root = document.getElementById('view');
  root.textContent = '';
  skeleton(root);

  try {
    await loadLookups();
    root.textContent = '';
    await views[state.view](root);
  } catch (err) {
    root.textContent = '';
    if (err.status === 401) { rendering = false; return showGate(false); }
    root.appendChild(el('div', { class: 'blank' }, [
      el('span', { class: 'legend', text: 'Could not read the control plane' }),
      el('p', { class: 'note', text: err.message }),
      el('p', null, [el('button', { type: 'button', class: 'btn-quiet', text: 'Try again', onclick: () => render() })]),
    ]));
  } finally {
    rendering = false;
  }
}

function showGate(needsSetup) {
  document.getElementById('app').hidden = true;
  const gate = document.getElementById('gate');
  gate.hidden = false;

  document.getElementById('gate-title').textContent = needsSetup ? 'Create the first administrator' : 'Sign in';
  document.getElementById('gate-hint').textContent = needsSetup
    ? 'This deployment has no administrator yet. Choose credentials — the password must be at least 12 characters.'
    : '';
  document.getElementById('gate-form').dataset.setup = needsSetup ? '1' : '';
  gate.querySelector('input[name=username]').focus();
}

function showApp() {
  document.getElementById('gate').hidden = true;
  document.getElementById('app').hidden = false;
  document.getElementById('whoami').textContent = `${state.user.username} · ${state.user.role}`;
  render();
}

document.getElementById('gate-form').addEventListener('submit', async (e) => {
  e.preventDefault();
  const form = e.target;
  const errBox = document.getElementById('gate-error');
  errBox.hidden = true;

  // Captured before the form is locked; FormData omits disabled controls.
  const data = new FormData(form);
  submitting(form, true);

  const path = form.dataset.setup ? '/api/bootstrap' : '/api/session';

  try {
    state.user = await api('POST', path, {
      username: data.get('username'),
      password: data.get('password'),
    });
    form.reset();
    submitting(form, false);
    showApp();
  } catch (err) {
    errBox.textContent = err.message;
    errBox.hidden = false;
    submitting(form, false);
  }
});

document.querySelector('#gate-form button[type=submit]').dataset.label = 'Continue';

document.getElementById('signout').addEventListener('click', async () => {
  try { await api('DELETE', '/api/session'); } catch (_) { /* sign out regardless */ }
  state.user = null;
  state.linked = new Set();
  state.seenLink = false;
  showGate(false);
});

document.getElementById('tabs').addEventListener('click', (e) => {
  const button = e.target.closest('button[data-view]');
  if (!button) return;
  state.view = button.dataset.view;
  for (const b of document.querySelectorAll('#tabs button')) {
    b.classList.toggle('active', b === button);
    b.setAttribute('aria-current', b === button ? 'page' : 'false');
  }
  render();
});

(async function boot() {
  try {
    state.user = await api('GET', '/api/session');
    showApp();
  } catch (_) {
    try {
      const setup = await api('GET', '/api/bootstrap');
      showGate(!!(setup && setup.needs_setup));
    } catch (_err) {
      showGate(false);
    }
  }
})();
