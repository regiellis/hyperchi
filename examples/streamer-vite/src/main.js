import './style.css'

// Side-effect import: sets window.htmx. Must run before htmx-anime.js below
// since htmx-anime-extensions.js calls htmx.registerExtension(...) at top level.
import './htmx-bootstrap.js'

// HyperChi declarative directives
import {
	start as hcStart,
	registerComponent,
} from '../../../frontend/assets/scripts/hyperchi-directives.ts'

// Animation system (HTMX + AnimeJS integration). window.htmx is already set
// by the bootstrap import above.
import '../static/js/htmx-anime.js'

// View transitions driven by data-vt-name, data-vt-animate, and data-vt-persist.
// Pairs with htmx.config.transitions=true in the htmx-config meta tag and the
// view-transition keyframes in style.css.
import '../../../frontend/assets/scripts/hyperchi-view-transitions.js'

// The app component holds the theme, the expanded row, and the open modal.
// All interactivity is declarative: hc-show drives details/modal visibility,
// HTMX hx-trigger="... once" loads fragments lazily.
registerComponent('app', () => ({
	darkMode: true,        // hc-persist hydrates from localStorage; default for first visit
	expandedId: null,      // currently-expanded row id, or null
	modalId: null,         // currently-open modal id, or null

	toggleTheme() {
		this.darkMode = !this.darkMode
	},
}))

// Boot the directive engine. Walks the document, wires reactive bindings,
// observes future DOM changes (including HTMX swaps).
hcStart()

console.log('HyperChi Streamer initialized (declarative directives + HTMX 4 + Tailwind)')
