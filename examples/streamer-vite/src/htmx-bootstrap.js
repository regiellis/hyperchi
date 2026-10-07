// Side-effect-only module: assigns htmx to window so subsequent script
// imports can reference htmx as a global from their top-level body.
//
// ESM evaluates sibling imports in source order, so importing this BEFORE
// any module that does `htmx.registerExtension(...)` at top level guarantees
// window.htmx is set in time.
import htmx from 'htmx.org'
window.htmx = htmx
