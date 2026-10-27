// Opt-in local navigation hooks. No transport, storage, cookies, or identifiers.
(() => {
  'use strict';
  if (window.HyperChiAnalytics) return;
  let enabled = false;
  let previous = null;
  const permitted = () => enabled && navigator.globalPrivacyControl !== true &&
    navigator.doNotTrack !== '1' && window.doNotTrack !== '1';

  function navigation(source = 'manual') {
    if (!permitted()) return false;
    const marker = document.querySelector('[data-hc-analytics-page]');
    const page = marker?.getAttribute('data-hc-analytics-page');
    // Explicit stable labels only. Never infer a label from URLs or titles.
    if (!page || !/^[a-zA-Z][a-zA-Z0-9_.:-]{0,79}$/.test(page)) return false;
    const current = location.pathname + location.search;
    if (current === previous) return false;
    previous = current; // Used locally for deduplication; never emitted.
    const detail = Object.freeze({type: 'navigation', page, source});
    document.dispatchEvent(new CustomEvent('hyperchi:analytics', {detail}));
    return true;
  }

  window.HyperChiAnalytics = Object.freeze({
    setEnabled(value) {
      enabled = value === true;
      if (!enabled) { previous = null; return false; }
      navigation('initial');
      return permitted();
    },
    navigation: () => navigation('manual'),
    isEnabled: permitted,
  });
  document.addEventListener('DOMContentLoaded', () => navigation('initial'));
  // HTMX 4 updates history before swapping; listen after the new marker exists.
  // Fragment-only updates at the same URL do not count as another navigation.
  document.addEventListener('htmx:after:swap', () => navigation('htmx'));
  window.addEventListener('pageshow', (event) => {
    if (event.persisted) { previous = null; navigation('restore'); }
  });
})();
