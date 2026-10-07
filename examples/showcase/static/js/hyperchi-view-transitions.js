/**
 * HyperChi View Transitions
 *
 * Data attributes for named, animated, and persisted view transitions, on top
 * of the browser's View Transitions API and htmx 4's transition wrapping.
 *
 * Authoring:
 *
 *   <h1 data-vt-name="page-title">...</h1>
 *     - Sets style.viewTransitionName so the element morphs across swaps
 *       when its data-vt-name matches an element on the next page.
 *
 *   <article data-vt-animate="slide">...</article>
 *     - Adds a class hook (vt-animate-slide) the CSS keyframes target.
 *       Pair with the default stylesheet (or your own) for the actual
 *       transition keyframes.
 *
 *   <aside data-vt-persist>...</aside>
 *     - Skips the transition for this element. It gets viewTransitionName
 *       "vt-persist" (or data-vt-persist-name) on every render, so the
 *       browser sees one element and does not animate it.
 *
 * Activation:
 *
 *   1. htmx.config.transitions = true. The scaffold's base template sets it
 *      in the htmx-config meta tag.
 *   2. This script runs on DOMContentLoaded and again after every
 *      htmx:after:swap / htmx:after:process so dynamically-loaded
 *      content picks up the same attributes.
 *
 * Browser support: the View Transitions API ships in Chromium-based
 * browsers and Safari. Elsewhere htmx swaps without animation, and
 * data-vt-name has no effect.
 */
(() => {
    const SCANNED = '__hcVtScanned';

    function applyTo(el) {
        // The same name on every render makes it one element to the browser,
        // which then does not animate it.
        if (el.hasAttribute('data-vt-persist')) {
            el.style.viewTransitionName = el.dataset.vtPersistName || 'vt-persist';
            return;
        }

        const name = el.dataset.vtName;
        if (name) {
            // viewTransitionName must be a CSS <custom-ident>. The value is
            // used as given; whitespace, which breaks it silently, only gets
            // a console warning.
            if (/\s/.test(name)) {
                console.warn('[hyperchi-vt] data-vt-name should be a single identifier (no spaces):', name, el);
            }
            el.style.viewTransitionName = name;
        }

        const animate = el.dataset.vtAnimate;
        if (animate) {
            // Class hook so user CSS / the default stylesheet can target it.
            el.classList.add(`vt-animate-${animate}`);
        }
    }

    function scan(root) {
        const node = root || document;
        // Element root: include itself if it matches.
        if (node.nodeType === 1 && node.matches?.('[data-vt-name],[data-vt-persist],[data-vt-animate]')) {
            applyTo(node);
            node[SCANNED] = true;
        }
        node.querySelectorAll?.('[data-vt-name],[data-vt-persist],[data-vt-animate]').forEach((el) => {
            if (el[SCANNED]) return;
            applyTo(el);
            el[SCANNED] = true;
        });
    }

    function init() {
        scan(document);
        // htmx 4 fires htmx:after:swap after content is in the DOM and
        // htmx:after:process after htmx finishes processing a node tree.
        // Both can introduce new data-vt-* attributes, so scan on each.
        document.body?.addEventListener('htmx:after:swap', (e) => scan(e.detail?.ctx?.target));
        document.body?.addEventListener('htmx:after:process', (e) => scan(e.target || document));
    }

    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', init);
    } else {
        init();
    }

    // Expose a manual hook for callers that mutate the DOM outside htmx
    // (e.g. directive expressions that swap content imperatively).
    if (typeof window !== 'undefined') {
        window.HyperChiViewTransitions = { scan };
    }
})();
