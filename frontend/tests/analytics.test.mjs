import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {runInNewContext} from 'node:vm';

const script = readFileSync(new URL('../assets/scripts/hyperchi-analytics.js', import.meta.url), 'utf8');
function setup({dnt, gpc, page = 'home'} = {}) {
  const listeners = new Map();
  const events = [];
  const addEventListener = (name, fn) => {
    if (!listeners.has(name)) listeners.set(name, []);
    listeners.get(name).push(fn);
  };
  const fire = (name, event = {}) => {for (const fn of listeners.get(name) || []) fn(event);};
  const document = {addEventListener,
    querySelector: () => page === null ? null : {getAttribute: () => page},
    dispatchEvent: event => {events.push(event.detail); fire(event.type, event);},
  };
  const window = {addEventListener};
  const navigator = {doNotTrack: dnt, globalPrivacyControl: gpc};
  const location = {pathname: '/', search: '?secret=private'};
  const context = {document, window, navigator, location,
    CustomEvent: class {constructor(type, {detail}) {this.type = type; this.detail = detail;}},
  };
  runInNewContext(script, context);
  return {api: window.HyperChiAnalytics, events, fire, navigator, location,
    setPage: value => {page = value;}, reload: () => runInNewContext(script, context)};
}

test('inert until explicitly enabled, emits only an immutable page label', () => {
  const env = setup();
  env.fire('DOMContentLoaded'); env.fire('htmx:after:swap');
  assert.equal(env.events.length, 0);
  assert.equal(env.api.setEnabled(true), true);
  assert.equal(JSON.stringify(env.events[0]), '{"type":"navigation","page":"home","source":"initial"}');
  assert(Object.isFrozen(env.events[0]));
  assert(!JSON.stringify(env.events).includes('secret'));
  env.api.setEnabled(true); env.api.navigation(); env.fire('DOMContentLoaded'); env.reload();
  assert.equal(env.events.length, 1);
});

test('htmx navigation deduplicates swaps and ignores fragments at the same URL', () => {
  const env = setup(); env.api.setEnabled(true);
  env.fire('htmx:after:swap'); assert.equal(env.events.length, 1);
  env.location.pathname = '/articles/private-id'; env.setPage('article');
  env.fire('htmx:after:swap'); env.fire('htmx:after:swap');
  assert.equal(env.events.length, 2); assert.equal(env.events[1].source, 'htmx');
  assert.equal(env.events[1].page, 'article');
  env.location.pathname = '/'; env.setPage('home'); env.fire('htmx:after:swap');
  assert.equal(env.events.length, 3); // History restoration through HTMX.
});

test('revocation drops events without replaying intervening navigation', () => {
  const env = setup(); env.api.setEnabled(true); env.api.setEnabled(false);
  env.location.pathname = '/one'; env.fire('htmx:after:swap');
  env.location.pathname = '/two'; env.fire('htmx:after:swap');
  assert.equal(env.events.length, 1);
  env.api.setEnabled(true); assert.equal(env.events.length, 2);
  env.fire('pageshow', {persisted: true}); assert.equal(env.events.length, 3);
});

for (const option of [{dnt: '1'}, {gpc: true}]) test(`privacy signal ${JSON.stringify(option)} prevents activation`, () => {
  const env = setup(option);
  assert.equal(env.api.setEnabled(true), false);
  env.fire('htmx:after:swap'); env.api.navigation();
  assert.equal(env.events.length, 0);
});

test('privacy signals are rechecked before each event', () => {
  const env = setup(); env.api.setEnabled(true);
  env.navigator.globalPrivacyControl = true; env.location.pathname = '/next';
  assert.equal(env.api.navigation(), false); assert.equal(env.events.length, 1);
});

test('missing or unsafe labels are never derived from a URL', () => {
  const env = setup({page: null}); env.api.setEnabled(true);
  for (const label of ['', '/users/private', 'email@example.test', 'x'.repeat(81)]) {
    env.setPage(label); env.api.navigation();
  }
  assert.equal(env.events.length, 0);
  env.setPage('article'); assert.equal(env.api.navigation(), true);
});
