/**
 * htmx and AnimeJS
 *
 * Runs AnimeJS animations on htmx swaps, declared in markup or sent by the
 * server.
 *
 * Features:
 * - Auto-animate on HTMX swaps with data-anime attribute
 * - Trigger animations via HTMX HX-Trigger headers
 * - Preset animations via data-anime-preset
 * - HTMX 4 extensions for common patterns (modal, list, fade, scroll)
 *
 * Usage:
 * 1. Via data-anime attribute:
 *    <div data-anime='{"opacity": [0,1], "duration": 600}'>Content</div>
 *
 * 2. Via HX-Trigger header (server-side):
 *    c.HX().Trigger("animateIn", hyperchi.H{"targets": "#content", "opacity": []int{0, 1}})
 *
 * 3. Via preset:
 *    <div data-anime-preset="fadeIn">Content</div>
 *
 * 4. Via HTMX extensions:
 *    <div data-anime-extension="modal">Modal with animations</div>
 *    <ul data-anime-extension="list">Animated list items</ul>
 */

import { animate, stagger } from 'animejs';
import './htmx-anime-extensions.js'; // Load HTMX extensions

// Presets. Keep them in step with AnimationPresets in hyperchi/animation.go.
const presets = {
    // Page Enter Animations
    pageSlideInFromLeft: (targets) => ({
        targets,
        translateX: [-100, 0],
        opacity: [0, 1],
        duration: 400,
        ease: 'outCubic'
    }),
    pageSlideInFromRight: (targets) => ({
        targets,
        translateX: [100, 0],
        opacity: [0, 1],
        duration: 400,
        ease: 'outCubic'
    }),
    pageSlideInFromTop: (targets) => ({
        targets,
        translateY: [-100, 0],
        opacity: [0, 1],
        duration: 400,
        ease: 'outCubic'
    }),
    pageSlideInFromBottom: (targets) => ({
        targets,
        translateY: [100, 0],
        opacity: [0, 1],
        duration: 400,
        ease: 'outCubic'
    }),
    pageFadeIn: (targets) => ({
        targets,
        opacity: [0, 1],
        duration: 300,
        ease: 'out'
    }),
    pageScaleIn: (targets) => ({
        targets,
        scale: [0.95, 1],
        opacity: [0, 1],
        duration: 300,
        ease: 'out'
    }),

    // Page Exit Animations
    pageSlideOutToLeft: (targets) => ({
        targets,
        translateX: [0, -100],
        opacity: [1, 0],
        duration: 300,
        ease: 'inCubic'
    }),
    pageSlideOutToRight: (targets) => ({
        targets,
        translateX: [0, 100],
        opacity: [1, 0],
        duration: 300,
        ease: 'inCubic'
    }),
    pageSlideOutToTop: (targets) => ({
        targets,
        translateY: [0, -100],
        opacity: [1, 0],
        duration: 300,
        ease: 'inCubic'
    }),
    pageSlideOutToBottom: (targets) => ({
        targets,
        translateY: [0, 100],
        opacity: [1, 0],
        duration: 300,
        ease: 'inCubic'
    }),
    pageFadeOut: (targets) => ({
        targets,
        opacity: [1, 0],
        duration: 200,
        ease: 'in'
    }),
    pageScaleOut: (targets) => ({
        targets,
        scale: [1, 0.95],
        opacity: [1, 0],
        duration: 200,
        ease: 'in'
    }),

    // Component Micro-Interactions
    componentHover: (targets) => ({
        targets,
        scale: [1, 1.05],
        duration: 200,
        ease: 'out'
    }),
    componentTap: (targets) => ({
        targets,
        scale: [1, 0.95],
        duration: 100,
        ease: 'out'
    }),
    componentFadeIn: (targets) => ({
        targets,
        opacity: [0, 1],
        duration: 200,
        ease: 'out'
    }),
    componentSlideIn: (targets) => ({
        targets,
        translateY: [20, 0],
        opacity: [0, 1],
        duration: 300,
        ease: 'out'
    }),

    // Utility
    stagger: (targets, delayMs = 100) => ({
        targets,
        opacity: [0, 1],
        translateY: [20, 0],
        duration: 400,
        delay: stagger(delayMs),
        ease: 'out'
    })
};

/**
 * Initialize animation on an element
 * @param {HTMLElement} element - Element to animate
 * @param {Object} config - AnimeJS configuration
 */
function initAnimation(element, config) {
    // anime.js v4 uses animate(targets, params); split the config so we can
    // call the v4 signature regardless of whether `targets` is in the config
    // (presets put it there) or whether we should fall back to `element`.
    const { targets, ...params } = config;
    try {
        animate(targets || element, params);
    } catch (error) {
        console.error('[HTMX-Anime] Animation error:', error);
    }
}

/**
 * Process elements with data-anime attribute
 * @param {HTMLElement} root - Root element to search from
 */
function processAnimations(root) {
    if (!root || typeof root.querySelectorAll !== 'function') return;
    // Process data-anime attributes
    root.querySelectorAll('[data-anime]').forEach(el => {
        try {
            const config = JSON.parse(el.getAttribute('data-anime'));
            initAnimation(el, config);
        } catch (error) {
            console.error('[HTMX-Anime] Failed to parse data-anime:', error);
        }
    });

    // Process data-anime-preset attributes
    root.querySelectorAll('[data-anime-preset]').forEach(el => {
        const presetName = el.getAttribute('data-anime-preset');
        const preset = presets[presetName];

        if (preset) {
            initAnimation(el, preset(el));
        } else {
            console.warn(`[HTMX-Anime] Unknown preset: ${presetName}`);
        }
    });
}

/**
 * Initialize HTMX event listeners
 */
function initHTMX() {
    // Animate new content after HTMX swaps. htmx 4 sets detail.ctx.target to the
    // hx-target selector string (not an element), so resolve it to the swap
    // destination; fall back to the event's source element.
    document.body.addEventListener('htmx:after:swap', (event) => {
        let root = event.detail?.ctx?.target;
        if (typeof root === 'string') root = document.querySelector(root);
        if (!(root instanceof Element)) root = event.target;
        if (root instanceof Element) processAnimations(root);
    });

    // Support for HX-Trigger animations
    // Server sends: HX-Trigger: {"animateIn": {"targets": "#id", "opacity": [0,1]}}
    document.body.addEventListener('animateIn', (event) => {
        const config = event.detail;
        if (!config) return;
        const { targets, ...params } = config;
        if (targets) animate(targets, params);
    });

    // Generic animation trigger
    document.body.addEventListener('animate', (event) => {
        const config = event.detail;
        if (!config) return;
        const { targets, ...params } = config;
        if (targets) animate(targets, params);
    });
}

/**
 * Export API for programmatic use
 */
export const HyperAnime = {
    animate,
    stagger,
    presets,
    init: initAnimation,
    process: processAnimations
};

/**
 * Auto-initialize on DOM ready
 */
if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', () => {
        initHTMX();
        processAnimations(document.body);
    });
} else {
    initHTMX();
    processAnimations(document.body);
}

// window.anime is the AnimeJS v4 animate function, called as
// animate(targets, params). The v3 form anime({targets, ...}) does not work.
window.HyperAnime = HyperAnime;
window.anime = animate;

export default HyperAnime;
