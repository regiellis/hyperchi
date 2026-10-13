/**
 * HTMX 4 + AnimeJS extensions.
 *
 * htmx 4 uses global extension registration. These extensions opt in
 * through data-anime-extension="modal|list|fade|scroll".
 */

import { animate, stagger } from 'animejs';

const extensionSelector = (name) => `[data-anime-extension~="${name}"]`;

const hasExtension = (elt, name) =>
    elt?.matches?.(extensionSelector(name)) ||
    elt?.closest?.(extensionSelector(name));

const swapText = async (ctx, swap) => {
    await htmx.swap({
        text: ctx.response?.text ?? ctx.text ?? '',
        sourceElement: ctx.sourceElement,
        target: ctx.target,
        swap: swap || ctx.swap,
        select: ctx.select,
        selectOOB: ctx.selectOOB,
        transition: ctx.transition,
        push: ctx.push,
        replace: ctx.replace,
        anchor: ctx.anchor,
    });
};

htmx.registerExtension('anime-modal', {
    htmx_after_swap: (_elt, detail) => {
        const target = detail.ctx?.target;
        if (!hasExtension(target, 'modal')) return;

        const modal = target.querySelector?.('[role="dialog"]') || target;
        const backdrop = modal.querySelector?.('.modal-backdrop');
        const enterPreset = modal.getAttribute('data-modal-enter') || 'default';

        if (backdrop) {
            animate(backdrop, {
                opacity: [0, 1],
                duration: 200,
                ease: 'outQuad',
            });
        }

        const animations = {
            default: {
                scale: [0.95, 1],
                opacity: [0, 1],
                translateY: [20, 0],
                duration: 300,
                ease: 'outCubic',
            },
            slideDown: {
                translateY: [-100, 0],
                opacity: [0, 1],
                duration: 400,
                ease: 'outCubic',
            },
            zoom: {
                scale: [0.8, 1],
                opacity: [0, 1],
                duration: 300,
                ease: 'outBack',
            },
        };

        animate(modal, animations[enterPreset] || animations.default);
    },
});

htmx.registerExtension('anime-list', {
    htmx_before_swap: (_elt, detail) => {
        const target = detail.ctx?.target;
        if (!hasExtension(target, 'list')) return;

        const positions = Array.from(target.children).map((item) => ({
            id: item.id || item.dataset.id,
            pos: item.getBoundingClientRect(),
        }));
        target.dataset.listPositions = JSON.stringify(positions);
    },

    htmx_after_swap: (_elt, detail) => {
        const target = detail.ctx?.target;
        if (!hasExtension(target, 'list')) return;

        const delay = parseInt(target.getAttribute('data-list-stagger'), 10) || 50;
        const duration = parseInt(target.getAttribute('data-list-duration'), 10) || 400;
        const newItems = Array.from(target.children);
        const oldPositions = target.dataset.listPositions
            ? JSON.parse(target.dataset.listPositions)
            : [];

        if (oldPositions.length > 0) {
            newItems.forEach((item) => {
                const id = item.id || item.dataset.id;
                const oldPos = oldPositions.find((p) => p.id === id);

                if (oldPos) {
                    const currentRect = item.getBoundingClientRect();
                    const deltaX = oldPos.pos.left - currentRect.left;
                    const deltaY = oldPos.pos.top - currentRect.top;

                    if (Math.abs(deltaX) > 0.5 || Math.abs(deltaY) > 0.5) {
                        animate(item, {
                            translateX: [deltaX, 0],
                            translateY: [deltaY, 0],
                            duration,
                            ease: 'outQuad',
                        });
                    }
                } else {
                    animate(item, {
                        opacity: [0, 1],
                        translateY: [20, 0],
                        duration,
                        delay: stagger(delay),
                        ease: 'outQuad',
                    });
                }
            });
        } else {
            animate(newItems, {
                opacity: [0, 1],
                translateY: [20, 0],
                duration,
                delay: stagger(delay),
                ease: 'outQuad',
            });
        }

        delete target.dataset.listPositions;
    },
});

htmx.registerExtension('anime-fade', {
    htmx_before_swap: (_elt, detail) => {
        const { ctx, tasks } = detail;
        const target = ctx?.target;
        if (!hasExtension(target, 'fade')) return;

        tasks.splice(0, tasks.length);

        const duration = parseInt(target.getAttribute('data-fade-duration'), 10) || 300;
        const outDuration = parseInt(target.getAttribute('data-fade-out-duration'), 10) || duration;

        animate(target, {
            opacity: [1, 0],
            duration: outDuration,
            ease: 'outQuad',
            complete: () => {
                swapText(ctx).then(() => {
                    animate(target, {
                        opacity: [0, 1],
                        duration,
                        ease: 'inQuad',
                    });
                });
            },
        });
    },
});

htmx.registerExtension('anime-scroll', {
    // htmx 4 hook key for htmx:after:process, which fires after htmx
    // finishes processing a node, including the initial document body.
    htmx_after_process: () => {
        initializeScrollAnimations(document);
    },
    htmx_after_swap: () => {
        initializeScrollAnimations(document);
    },
});

function initializeScrollAnimations(root) {
    root.querySelectorAll(extensionSelector('scroll')).forEach((target) => {
        if (target.dataset.animeScrollInitialized === 'true') return;
        target.dataset.animeScrollInitialized = 'true';

        const preset = target.getAttribute('data-scroll-preset') || 'componentFadeIn';
        const threshold = parseFloat(target.getAttribute('data-scroll-threshold')) || 0.2;
        const once = target.getAttribute('data-scroll-once') !== 'false';

        const presets = {
            fadeIn: { opacity: [0, 1], duration: 600, ease: 'outQuad' },
            slideUp: { opacity: [0, 1], translateY: [40, 0], duration: 600, ease: 'outCubic' },
            slideLeft: { opacity: [0, 1], translateX: [40, 0], duration: 600, ease: 'outCubic' },
            scaleIn: { opacity: [0, 1], scale: [0.9, 1], duration: 600, ease: 'outCubic' },
            componentFadeIn: { opacity: [0, 1], translateY: [20, 0], duration: 400, ease: 'outQuad' },
        };

        target.style.opacity = '0';

        const observer = new IntersectionObserver((entries) => {
            entries.forEach((entry) => {
                if (entry.isIntersecting) {
                    animate(entry.target, presets[preset] || presets.componentFadeIn);
                    if (once) observer.unobserve(entry.target);
                } else if (!once) {
                    entry.target.style.opacity = '0';
                }
            });
        }, { threshold, rootMargin: '0px' });

        observer.observe(target);
    });
}

export const HyperAnimeExtensions = {
    modal: 'modal',
    list: 'list',
    fade: 'fade',
    scroll: 'scroll',
};

export default HyperAnimeExtensions;
