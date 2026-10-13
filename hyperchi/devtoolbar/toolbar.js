(() => {
  'use strict';
  if (document.getElementById('hyperchi-devtoolbar')) return;
  const base = '/__hyperchi/devtools';
  const host = document.createElement('div');
  host.id = 'hyperchi-devtoolbar';
  const root = host.attachShadow({ mode: 'open' });
  const make = (tag, text, parent) => {
    const node = document.createElement(tag);
    if (text) node.textContent = text;
    if (parent) parent.append(node);
    return node;
  };
  const css = make('link', '', root);
  css.rel = 'stylesheet'; css.href = `${base}/toolbar.css`;
  const toggle = make('button', 'HC · Dev tools', root);
  toggle.className = 'launcher';
  toggle.setAttribute('aria-expanded', 'false');
  toggle.setAttribute('aria-controls', 'panel');
  const panel = make('section', '', root);
  panel.id = 'panel'; panel.hidden = true;
  panel.setAttribute('aria-label', 'HyperChi developer tools');
  const header = make('header', '', panel);
  make('strong', 'HyperChi', header);
  make('span', 'LOCAL DEVELOPMENT', header).className = 'badge';
  const close = make('button', 'Close', header);
  const nav = make('nav', '', panel);
  nav.setAttribute('aria-label', 'Developer tool views');
  const views = ['Requests', 'Routes', 'Inspector'];
  const buttons = views.map(name => make('button', name, nav));
  const refresh = make('button', 'Refresh', nav);
  const status = make('p', '', panel);
  status.className = 'status'; status.setAttribute('role', 'status');
  const content = make('div', '', panel); content.className = 'content';
  let active = 'Requests';
  let snapshot = { requests: [], routes: [] };
  let selection = 'Choose “Pick element”, then click an element on the page. Escape cancels.';
  let inspecting = false;
  const stopInspecting = () => {
    inspecting = false;
    document.removeEventListener('click', pickElement, true);
  };
  const pickElement = event => {
    if (event.composedPath().includes(host)) return;
    const element = event.composedPath().find(node => node instanceof Element);
    if (!element) return;
    event.preventDefault(); event.stopImmediatePropagation();
    const rect = element.getBoundingClientRect();
    const style = getComputedStyle(element);
    selection = JSON.stringify({
      tag: element.tagName.toLowerCase(), id: element.id,
      classes: [...element.classList], role: element.getAttribute('role'),
      width: Math.round(rect.width), height: Math.round(rect.height),
      display: style.display, fontSize: style.fontSize,
      padding: style.padding, margin: style.margin,
    }, null, 2);
    stopInspecting(); render();
  };
  const render = () => {
    content.replaceChildren();
    buttons.forEach((button, i) => button.setAttribute('aria-pressed', String(views[i] === active)));
    if (active === 'Inspector') {
      const pick = make('button', inspecting ? 'Cancel picking' : 'Pick element', content);
      pick.addEventListener('click', () => {
        if (inspecting) stopInspecting();
        else { inspecting = true; document.addEventListener('click', pickElement, true); }
        render();
      });
      make('pre', selection, content);
      return;
    }
    const rows = active === 'Requests' ? snapshot.requests : snapshot.routes;
    if (!rows.length) { make('p', active === 'Requests' ? 'No completed local requests yet.' : 'No registered routes.', content); return; }
    const table = make('table', '', content);
    make('caption', active === 'Requests' ? 'Completed requests · newest first. Select a row for details.' : 'Registered routes · GET also answers HEAD.', table);
    const head = make('tr', '', make('thead', '', table));
    const columns = active === 'Requests' ? ['Request', 'Status', 'Time', 'Bytes'] : ['Method', 'Pattern'];
    columns.forEach(name => { const th = make('th', name, head); th.scope = 'col'; });
    const body = make('tbody', '', table);
    rows.forEach(row => {
      const tr = make('tr', '', body);
      if (active === 'Routes') {
        make('td', row.method, tr); make('td', row.pattern, tr); return;
      }
      const button = make('button', `${row.method} ${row.route}`, make('td', '', tr));
      button.className = 'request';
      button.addEventListener('click', () => {
        active = 'Inspector'; selection = JSON.stringify(row, null, 2); render();
      });
      make('td', String(row.status), tr);
      make('td', `${row.durationMS.toFixed(1)} ms`, tr);
      make('td', String(row.bytes), tr);
    });
  };
  const load = async () => {
    refresh.disabled = true;
    try {
      const response = await fetch(`${base}/data`, { credentials: 'same-origin', cache: 'no-store' });
      if (!response.ok) throw new Error(`HTTP ${response.status}`);
      snapshot = await response.json();
      status.textContent = `${snapshot.requests.length} retained requests · query values, headers and bodies are not collected`;
      render();
    } catch (error) {
      status.textContent = `Developer tools unavailable (${error.message}). Use a direct localhost connection in development.`;
    } finally { refresh.disabled = false; }
  };
  const setOpen = open => {
    panel.hidden = !open; toggle.setAttribute('aria-expanded', String(open));
    if (open) { void load(); buttons[0].focus(); }
    else { stopInspecting(); toggle.focus(); }
  };
  toggle.addEventListener('click', () => setOpen(panel.hidden));
  close.addEventListener('click', () => setOpen(false));
  refresh.addEventListener('click', load);
  buttons.forEach((button, i) => button.addEventListener('click', () => {
    stopInspecting(); active = views[i]; render();
  }));
  document.addEventListener('keydown', event => {
    if (event.key !== 'Escape') return;
    if (inspecting) { stopInspecting(); render(); }
    else if (!panel.hidden) setOpen(false);
  });
  document.body.append(host);
  render();
})();
