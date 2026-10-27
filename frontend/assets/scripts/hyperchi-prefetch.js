/** Opt-in HTTP-cache warming. Source of truth; run task sync-runtime-assets. */
(() => {
  'use strict';
  if (window.HyperChiPrefetch) return;
  const selector = 'a[data-hc-prefetch]';
  const allowed = new Set(['hover', 'viewport', 'both']);
  const pending = new Map();
  const running = new Map();
  const recent = new Map();
  const hovered = new Set();
  const focused = new Set();
  const visible = new Set();
  const timers = new Map();
  let stopped = false;
  let refreshQueued = false;
  let observer;
  let mutations;
  const connection = navigator.connection;
  const reduced = window.matchMedia?.('(prefers-reduced-data: reduce)');
  const blocked = () => stopped || document.hidden || navigator.onLine === false ||
    connection?.saveData || /(^|-)2g$/.test(connection?.effectiveType || '') || reduced?.matches;
  const attr = (element, name) => {
    const names = [name, name.replace('hx-', 'data-hx-')];
    for (let node = element; node; node = node.parentElement) {
      for (const key of names) {
        if ((node === element || window.htmx?.config?.implicitInheritance) && node.hasAttribute(key)) return node.getAttribute(key);
        if (node.hasAttribute(`${key}:inherited`)) return node.getAttribute(`${key}:inherited`);
      }
    }
    return null;
  };
  const strategy = element => element?.getAttribute?.('data-hc-prefetch');
  const interested = element => ((hovered.has(element) || focused.has(element)) && /^(hover|both)$/.test(strategy(element))) ||
    (visible.has(element) && /^(viewport|both)$/.test(strategy(element)));
  const identifier = element => `${element.tagName.toLowerCase()}${element.id ? '#' + encodeURI(element.id) : ''}`;
  function requestFor(element) {
    if (!element?.isConnected || !element.matches(selector) || !allowed.has(strategy(element))) return null;
    if (element.hasAttribute('download') || element.hasAttribute('ping') ||
        (element.target && element.target !== '_self') || element.closest('[data-hc-prefetch="off"]') ||
        document.querySelector('base[target]')) return null;
    if (window.htmx?.config?.prefix && window.htmx.config.prefix !== 'data-hx-') return null;
    for (let node = element; node; node = node.parentElement) {
      if ([...node.attributes].some(a => /^(data-)?hx-/.test(a.name) && (a.name.includes(':append') || /^(data-)?hx-on/.test(a.name)))) return null;
    }
    // Dynamic request inputs and customized inheritance cannot be predicted.
    for (const name of ['hx-post', 'hx-put', 'hx-patch', 'hx-delete', 'hx-method', 'hx-action',
      'hx-headers', 'hx-vals', 'hx-include', 'hx-params', 'hx-config', 'hx-confirm', 'hx-disable',
      'hx-inherit', 'hx-disinherit', 'hx-ext']) {
      if (attr(element, name) !== null) return null;
    }
    const get = attr(element, 'hx-get');
    const boost = attr(element, 'hx-boost');
    if (boost !== null && boost !== 'true' && boost !== 'false') return null;
    const htmxRequest = get !== null || boost === 'true';
    if (htmxRequest && (!window.htmx || !element.hasAttribute('data-htmx-powered') ||
      (window.htmx.config?.metaCharacter && window.htmx.config.metaCharacter !== ':'))) return null;
    const action = get ?? element.getAttribute('href');
    if (!action || action.startsWith('#')) return null;
    let url;
    try { url = new URL(action, document.baseURI); } catch { return null; }
    if (!/^https?:$/.test(url.protocol) || url.origin !== location.origin || url.username || url.password) return null;
    url.hash = '';
    if (url.href === location.href.split('#')[0]) return null;
    const headers = { Accept: 'text/html' };
    if (htmxRequest) {
      // Ordinary hx-get and boolean hx-boost use htmx 4's core headers.
      // Custom request hooks remain responsible for declaring their own Vary.
      let target = boost === 'true' && get === null ? document.body : element;
      const targetSelector = attr(element, 'hx-target');
      if (targetSelector !== null) {
        if (targetSelector !== 'body' && !/^#[\w-]+$/.test(targetSelector)) return null;
        target = document.querySelector(targetSelector);
        if (!target) return null;
      }
      headers['HX-Request'] = 'true';
      headers['HX-Source'] = identifier(element);
      headers['HX-Current-URL'] = location.href;
      headers['HX-Target'] = identifier(target);
      headers['HX-Request-Type'] = target === document.body || attr(element, 'hx-select') ? 'full' : 'partial';
      if (boost === 'true' && get === null) headers['HX-Boosted'] = 'true';
    }
    return { url: url.href, headers, key: JSON.stringify([url.href, headers]) };
  }
  function remember(key) {
    recent.delete(key); recent.set(key, Date.now() + 30000);
    while (recent.size > 128) recent.delete(recent.keys().next().value);
  }
  async function run(request, element) {
    const controller = new AbortController();
    running.set(request.key, { controller, element });
    const timeout = setTimeout(() => controller.abort(), 5000);
    try {
      const response = await fetch(request.url, {
        method: 'GET', mode: 'same-origin', credentials: 'same-origin', redirect: 'error',
        headers: { ...request.headers, Purpose: 'prefetch' }, signal: controller.signal,
        cache: 'default', priority: 'low',
      });
      if (!response.ok || !/^text\/html\b/i.test(response.headers.get('Content-Type') || '') ||
          Number(response.headers.get('Content-Length')) > 1048576) {
        controller.abort(); return;
      }
      // Drain a bounded body so the browser can populate its HTTP cache.
      // No JS response cache: no-store, Vary and validators remain authoritative.
      if (!response.body) return;
      const reader = response.body.getReader();
      let bytes = 0;
      while (true) {
        const chunk = await reader.read();
        if (chunk.done) break;
        bytes += chunk.value.byteLength;
        if (bytes > 1048576) { controller.abort(); break; }
      }
    } catch { /* Speculation failure must never affect navigation. */ }
    finally { clearTimeout(timeout); running.delete(request.key); pump(); }
  }
  function pump() {
    if (blocked()) return;
    for (const [element] of pending) {
      if (running.size >= 2) break;
      pending.delete(element);
      if (!interested(element)) continue;
      const request = requestFor(element);
      if (!request || running.has(request.key) || recent.get(request.key) > Date.now()) continue;
      remember(request.key);
      void run(request, element);
    }
  }
  function schedule(element) {
    if (blocked() || timers.has(element) || pending.has(element) || !requestFor(element)) return;
    if (timers.size + pending.size >= 64) return;
    timers.set(element, setTimeout(() => {
      timers.delete(element);
      if (interested(element) && requestFor(element)) pending.set(element, true);
      pump();
    }, 100));
  }
  function cancel(element) {
    clearTimeout(timers.get(element)); timers.delete(element); pending.delete(element);
    for (const entry of running.values()) if (entry.element === element) entry.controller.abort();
  }
  function clear() {
    for (const timer of timers.values()) clearTimeout(timer);
    timers.clear(); pending.clear(); recent.clear();
    for (const entry of running.values()) entry.controller.abort();
  }
  function refresh() {
    if (stopped) return;
    for (const element of [...timers.keys(), ...pending.keys(), ...hovered, ...focused]) {
      if (!element.isConnected || !requestFor(element)) { cancel(element); hovered.delete(element); focused.delete(element); }
    }
    for (const [key, entry] of running) if (requestFor(entry.element)?.key !== key) entry.controller.abort();
    observer?.disconnect(); visible.clear();
    if (blocked()) { clear(); return; }
    let count = 0;
    document.querySelectorAll(selector).forEach(element => {
      if (count < 512 && /^(viewport|both)$/.test(strategy(element)) && requestFor(element)) {
        observer?.observe(element); count++;
      }
    });
  }
  function queueRefresh() {
    if (refreshQueued || stopped) return;
    refreshQueued = true;
    queueMicrotask(() => { refreshQueued = false; refresh(); });
  }
  function enter(event, focus = false) {
    if (stopped) return;
    if (event.pointerType === 'touch') return;
    const element = event.target.closest?.(selector);
    if (!element || element.contains(event.relatedTarget)) return;
    (focus ? focused : hovered).add(element); schedule(element);
  }
  function leave(event, focus = false) {
    const element = event.target.closest?.(selector);
    if (!element || element.contains(event.relatedTarget)) return;
    (focus ? focused : hovered).delete(element);
    if (!interested(element)) cancel(element);
  }
  function navigate(event) {
    const element = event.target.closest?.(selector);
    if (element) { hovered.delete(element); focused.delete(element); visible.delete(element); cancel(element); }
  }
  function beforeRequest(event) {
    // Clear speculative state before writes: a mutation can invalidate every
    // page. Cache freshness after a write is still the server's responsibility.
    const method = event.detail?.ctx?.request?.method?.toUpperCase();
    if (method && method !== 'GET' && method !== 'HEAD') clear();
    const element = event.detail?.ctx?.sourceElement;
    if (element) cancel(element);
  }
  function start() {
    if (stopped) return;
    if ('IntersectionObserver' in window) observer = new IntersectionObserver(entries => {
      for (const entry of entries) {
        if (entry.isIntersecting) { visible.add(entry.target); schedule(entry.target); }
        else { visible.delete(entry.target); if (!interested(entry.target)) cancel(entry.target); }
      }
    }, { rootMargin: '0px' });
    mutations = new MutationObserver(queueRefresh);
    mutations.observe(document.documentElement, { childList: true, subtree: true, attributes: true,
      attributeFilter: ['href', 'data-hc-prefetch', 'hx-get', 'hx-target', 'hx-boost', 'hx-boost:inherited', 'hx-target:inherited', 'data-hx-get', 'data-hx-target', 'data-hx-boost', 'data-hx-boost:inherited'] });
    refresh();
  }
  document.addEventListener('pointerover', enter);
  document.addEventListener('pointerout', leave);
  document.addEventListener('focusin', event => enter(event, true));
  document.addEventListener('focusout', event => leave(event, true));
  document.addEventListener('click', navigate, true);
  document.addEventListener('htmx:before:request', beforeRequest);
  document.addEventListener('htmx:after:swap', queueRefresh);
  document.addEventListener('htmx:after:process', queueRefresh);
  document.addEventListener('visibilitychange', queueRefresh);
  connection?.addEventListener('change', queueRefresh);
  reduced?.addEventListener('change', queueRefresh);
  window.addEventListener('offline', clear);
  window.addEventListener('online', queueRefresh);
  window.addEventListener('pagehide', clear);
  window.addEventListener('pageshow', queueRefresh);
  window.HyperChiPrefetch = Object.freeze({ refresh, clear, stop() {
    stopped = true; clear(); hovered.clear(); focused.clear(); visible.clear(); observer?.disconnect(); mutations?.disconnect();
  } });
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', start, { once: true });
  else start();
})();
