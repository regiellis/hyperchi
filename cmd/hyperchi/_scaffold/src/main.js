import './style.css'
import htmx from 'htmx.org'

// Expose htmx globally so the runtime scripts that base.tmpl loads with
// <script> tags (hyperchi-directives.js, hyperchi-view-transitions.js) and
// inline component registrations can reference it.
window.htmx = htmx
