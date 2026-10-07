---
title: Directives
section: Built-ins
order: 120
next: configuration
description: Some interactions belong in the browser. HyperChi’s `hc-*` attributes handle small pieces of local state alongside server-rendered HTML.
---

## Start with a scope {#overview}

Load the bundled `hyperchi-directives.js` runtime. Define state with `hc-data`, handle an event with `hc-on`, and render a value with `hc-text`.

```html
<script src="/static/js/hyperchi-directives.js"></script>

<div hc-data="{ count: 0 }">
  <button hc-on:click="count++">Add one</button>
  <output hc-text="count">0</output>
</div>

<script>HyperChi.start();</script>
```

<!-- slot:directives-demo -->

`hc-data` takes any JavaScript expression that returns an object. That object becomes a reactive scope for the element and everything inside it: a change to `count` updates every directive that reads it.

## Name a reusable component {#example}

Register a factory before starting HyperChi, then reference it from your HTML. A method named `init` runs once when the scope is created, with `this` set to the scope.

```js
HyperChi.registerComponent('counter', () => ({
  count: 0,
  increment() { this.count++ },
  init() { this.count = Number(sessionStorage.getItem('count')) || 0 },
}));
HyperChi.start();
```

```html
<div hc-data="counter()">
  <button hc-on:click="increment()">Add one</button>
  <output hc-text="count">0</output>
</div>
```

Anything you register is also visible in every expression on the page, so a helper such as `registerComponent('money', n => '$' + n.toFixed(2))` can be called as `money(price)`.

## Every directive {#reference}

The runtime implements these fifteen attributes and nothing else.

| Directive | What it does |
| --- | --- |
| `hc-data="expr"` | Creates a reactive scope from the object the expression returns. |
| `hc-init="stmt"` | Runs a statement once when the element is processed. |
| `hc-show="expr"` | Shows or hides the element with `display`; it stays in the DOM. |
| `hc-if="expr"` | Removes the element from the DOM when false and restores it when true. |
| `hc-for="item in items"` | Repeats the element per item; `item, i in items` also gives the index. |
| `hc-text="expr"` | Sets `textContent` to the value. |
| `hc-html="expr"` | Sets `innerHTML` to the value. See the security notes below. |
| `hc-bind:attr="expr"` | Binds an attribute to the value. Shorthand `:attr`. |
| `hc-on:event="stmt"` | Listens for an event. Shorthand `@event`. |
| `hc-model="key"` | Two-way binding between a form control and a scope key. |
| `hc-ref="name"` | Stores the element in `$refs.name`. |
| `hc-effect="stmt"` | Runs a statement now and again whenever what it reads changes. |
| `hc-transition="enter:a|leave:b"` | Adds a class while `hc-show` shows or hides the element. |
| `hc-persist="key:storageKey"` | Syncs a scope key with `localStorage`. |
| `hc-cloak` | Removed once the element is processed, so CSS can hide it until then. |

Class and style bindings take richer values. `hc-bind:class` accepts a string, an array, or an object of `{ name: condition }`, and `hc-bind:style` accepts an object of camelCase properties. A binding to `false` or nothing removes the attribute, and `true` sets it empty, which suits `disabled`.

```html
<div hc-data="{ open: false, items: ['Go', 'htmx'], name: '' }">
  <button hc-on:click.prevent="open = !open" hc-bind:aria-expanded="String(open)">Menu</button>
  <ul hc-show="open" hc-cloak>
    <li hc-for="item, i in items" hc-text="i + 1 + '. ' + item"></li>
  </ul>
  <input hc-model="name" placeholder="Your name">
  <button hc-bind:disabled="!name" hc-bind:class="{ ready: name }">Greet</button>
  <p hc-if="name" hc-text="'Hello, ' + name + '.'"></p>
</div>
```

Add `[hc-cloak] { display: none; }` to your stylesheet so cloaked elements stay hidden until the runtime reaches them.

## Events and modifiers {#events}

`hc-on:click`, `hc-on:input`, and every other DOM event work, as do custom and htmx events: `hc-on:htmx:after:swap="refresh($event)"`. A statement can read the event as `$event`. Chain modifiers after the event name with dots: `.prevent` calls `preventDefault`, `.stop` stops propagation, `.self` fires only when the element itself was the target, and `.once` removes the listener after the first call.

## Scopes nest {#scopes}

A child `hc-data` sees its own values and every ancestor's. Reading a name looks from the nearest scope outward, and assigning to a name that an ancestor already owns changes the ancestor's value. That lets a small component update page-level state, such as a theme flag set on `<html>`. `hc-for` gives each repeated element its own scope for the item variable.

`hc-persist="darkMode:theme"` keeps `darkMode` in `localStorage` under `theme`, reading it back when the scope is created. Without the second part, the key is `_hc_darkMode`.

## They survive htmx swaps {#htmx}

The runtime does not bind to the elements that exist at load. It watches the document for new nodes, and also runs after every htmx swap, so a fragment that arrives from `c.Fragment` or over a WebSocket is processed the moment it lands. Elements that are removed have their listeners and effects torn down. That is why the rule in HyperChi apps is `hc-on:click` and never an inline `onclick`: an inline handler has no scope to read, and a Content-Security-Policy without `'unsafe-inline'` blocks it outright.

Keep component state in a registered `hc-data` function, so a fragment can say `hc-data="tray()"` without shipping a script. After you change the DOM some other way, `HyperChi.refresh(element)` processes it again. `HyperChi.start({ root })` limits the runtime to one subtree.

## Security notes {#security}

- Expressions are compiled with `new Function`, so a page that uses directives needs `script-src 'unsafe-eval'` in its Content-Security-Policy. A stricter policy turns every directive off and logs an error to the console.
- Directive values come from your templates. Never build an `hc-*` attribute from visitor input.
- `hc-html` assigns to `innerHTML` with no sanitizing by default. Install one before `start()` if you bind it to anything not written by you: `HyperChi.config.sanitizeHTML = (html) => DOMPurify.sanitize(html)`. Prefer `hc-text` whenever the value is plain text.

## Use it with htmx {#next}

Pair the two by letting the server own data and the browser own presentation. A fragment returns HTML with `hc-*` attributes, htmx swaps it in, and the runtime wires it. For live regions, see [realtime](/docs/realtime), and for the server side of fragments, [templates](/docs/templates).

The documentation topic filter, the search shortcut, the copy buttons, and the theme switch on this page all use these directives.
