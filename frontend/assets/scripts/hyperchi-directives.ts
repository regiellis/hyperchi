/**
 * HyperChi directives: a small reactive runtime for hc-* attributes. It uses
 * @vue/reactivity (about 6 KB gzipped) for dependency tracking; the directive
 * walker, expression compiler, lifecycle, and htmx integration live here.
 *
 * Directives:
 *   hc-data="expr"         create a reactive scope (component root)
 *   hc-init="stmt"         run once after scope is created
 *   hc-show="expr"         toggle display (none vs. previous)
 *   hc-text="expr"         reactive textContent
 *   hc-html="expr"         reactive innerHTML (XSS surface: see Security below)
 *   hc-bind:foo="expr"     reactive attribute binding (alias: :foo)
 *   hc-on:event="stmt"     event listener (alias: @event)
 *   hc-cloak               removed once processing reaches the element
 *   hc-ref="name"          register element in scope.$refs
 *   hc-effect="stmt"       run a reactive side-effect block
 *   hc-if="expr"           conditional render (element removed from DOM when falsy)
 *   hc-for="item in items" repeat element per item (clones template, scoped vars)
 *   hc-model="key"         two-way form binding (input/textarea/select/checkbox/radio)
 *   hc-transition="cfg"    enter/leave class transitions paired with hc-show
 *                          format: "enter:cls1|leave:cls2" (either side optional)
 *   hc-persist="key[:ls]"  sync scope[key] to localStorage (default ls key: _hc_key)
 *
 * Event modifiers (work on @event and hc-on:event):
 *   .self     fire only when event.target === event.currentTarget
 *   .prevent  call event.preventDefault()
 *   .stop     call event.stopPropagation()
 *   .once     remove the listener after the first invocation
 *
 * Usage:
 *   import { start, registerComponent } from './hyperchi-directives'
 *   registerComponent('app', () => ({ darkMode: false, init() { ... } }))
 *   start()
 *
 * Element <... hc-data="app()"> creates a reactive instance of `app()` as the
 * scope for that subtree. Scopes are looked up via parent traversal, so any
 * descendant can reference `darkMode` directly in expressions.
 *
 * Security:
 *   Content-Security-Policy: expressions are compiled with `new Function` and
 *   evaluated inside `with` blocks, so pages that use directives need
 *   `script-src 'unsafe-eval'`. HyperChi's Security middleware sends that by
 *   default in production; a stricter CSP_POLICY without it disables every
 *   directive (expressions fail to compile and log to the console).
 *   hc-html: the expression's value is assigned to innerHTML. It is not
 *   sanitized unless you install a sanitizer, so never bind it to user input
 *   without one:
 *     HyperChi.config.sanitizeHTML = (html) => DOMPurify.sanitize(html)
 *   The default is the identity function.
 */

import { reactive, effect, stop, type ReactiveEffectRunner } from '@vue/reactivity'

// ============================================================================
// Types
// ============================================================================

export type Scope = Record<string, any>
export type ComponentFn = () => Scope

interface ElementState {
	scope?: Scope
	effects: ReactiveEffectRunner[]
	removers: Array<() => void>
	boundClasses?: Set<string>
	boundStyles?: Set<string>
	processed?: boolean
	transition?: { enter?: string; leave?: string }
	// Structural directives mark themselves to skip normal processing on the template element.
	structural?: boolean
}

// ============================================================================
// Internal state
// ============================================================================

const states = new WeakMap<Element, ElementState>()
const exprCache = new Map<string, Function>()
const stmtCache = new Map<string, Function>()

// $globals is the outer `with` scope for every compiled expression. Component
// factories registered via registerComponent() land here so `hc-data="app()"`
// resolves cleanly without polluting window.
const $globals: Record<string, any> = {}

// ============================================================================
// Public API
// ============================================================================

let started = false

/** Runtime settings. Assign to fields before or after start(). */
export const config: {
	/**
	 * Applied to every hc-html value before it is assigned to innerHTML.
	 * Defaults to the identity function: no sanitizing. Install a sanitizer
	 * (DOMPurify, or the browser's Sanitizer API) before binding hc-html to
	 * anything a user can influence.
	 */
	sanitizeHTML: (html: string) => string
} = {
	sanitizeHTML: (html) => html,
}

export function start(opts?: { root?: Element }): void {
	if (started) return
	started = true
	// Default to documentElement (<html>) so directives on the root element
	// (e.g. hc-data="app()" on <html>) are processed.
	const root = opts?.root ?? document.documentElement
	walk(root)
	observe(root)
	hookHtmx()
}

export function registerComponent(name: string, fn: ComponentFn): void {
	$globals[name] = fn
}

/** Manually re-process a subtree (useful after non-HTMX DOM injection). */
export function refresh(root: Element = document.body): void {
	walk(root)
}

// ============================================================================
// Expression compilation
// ============================================================================

function compileExpr(src: string): (scope: any) => any {
	let fn = exprCache.get(src)
	if (!fn) {
		try {
			// `with` is non-strict only, but new Function bodies default to
			// non-strict so template expressions can reference scope fields
			// without a prefix.
			fn = new Function(
				'$scope',
				'$globals',
				`with ($globals) { with ($scope) { return (${src}) } }`,
			)
		} catch (err) {
			console.error(`[hyperchi] expression failed to compile: ${src}`, err)
			fn = () => undefined
		}
		exprCache.set(src, fn)
	}
	return (scope: any) => {
		try {
			return fn!(scope, $globals)
		} catch (err) {
			console.error(`[hyperchi] expression error: ${src}`, err)
			return undefined
		}
	}
}

function compileStmt(src: string): (scope: any, ev: Event) => any {
	let fn = stmtCache.get(src)
	if (!fn) {
		try {
			fn = new Function(
				'$scope',
				'$globals',
				'$event',
				`with ($globals) { with ($scope) { ${src} } }`,
			)
		} catch (err) {
			console.error(`[hyperchi] statement failed to compile: ${src}`, err)
			fn = () => {}
		}
		stmtCache.set(src, fn)
	}
	return (scope: any, ev: Event) => {
		try {
			return fn!(scope, $globals, ev)
		} catch (err) {
			console.error(`[hyperchi] statement error: ${src}`, err)
			return undefined
		}
	}
}

// ============================================================================
// Walk + process
// ============================================================================

function getState(el: Element): ElementState {
	let s = states.get(el)
	if (!s) {
		s = { effects: [], removers: [] }
		states.set(el, s)
	}
	return s
}

function findScope(el: Element): Scope | undefined {
	const scopes = findScopes(el)
	if (scopes.length === 0) return undefined
	if (scopes.length === 1) return scopes[0]
	return mergedScope(scopes)
}

/** Walk up the DOM collecting all scopes (outer-most first). */
function findScopes(el: Element): Scope[] {
	const scopes: Scope[] = []
	let cur: Element | null = el
	while (cur) {
		const s = states.get(cur)
		if (s?.scope) scopes.unshift(s.scope)
		cur = cur.parentElement
	}
	return scopes
}

/**
 * Build a single Scope-like proxy that delegates property access through a
 * chain of scopes, inner-most first. Reads on this proxy still go through the
 * underlying reactive scopes, so Vue's dependency tracking is preserved.
 *
 * Used for nested scopes (hc-for inside hc-data) where descendants need
 * access to both the iteration vars and the parent component state.
 */
function mergedScope(scopes: Scope[]): Scope {
	return new Proxy(
		{},
		{
			get(_, prop) {
				for (let i = scopes.length - 1; i >= 0; i--) {
					if (prop in scopes[i]) return (scopes[i] as any)[prop]
				}
				return undefined
			},
			has(_, prop) {
				return scopes.some((s) => prop in s)
			},
			set(_, prop, value) {
				for (let i = scopes.length - 1; i >= 0; i--) {
					if (prop in scopes[i]) {
						;(scopes[i] as any)[prop] = value
						return true
					}
				}
				;(scopes[scopes.length - 1] as any)[prop] = value
				return true
			},
		},
	) as Scope
}

function walk(el: Element): void {
	const s = states.get(el)
	if (!s?.processed) {
		// hc-if and hc-for walk what they render themselves. Walking on into a
		// hidden hc-if would mark its descendants processed without a scope,
		// so they would never bind once it shows.
		const structural = el.hasAttribute('hc-if') || el.hasAttribute('hc-for')
		process(el)
		if (structural) return
	}
	for (const child of Array.from(el.children)) walk(child)
}

function process(el: Element): void {
	const state = getState(el)
	state.processed = true

	// Structural directives short-circuit normal processing of this element:
	// they remove it from the DOM and clone/insert as needed.
	if (el.hasAttribute('hc-if')) {
		processIf(el)
		state.structural = true
		return
	}
	if (el.hasAttribute('hc-for')) {
		processFor(el)
		state.structural = true
		return
	}

	// hc-data first: it creates the scope for this subtree.
	const dataAttr = el.getAttribute('hc-data')
	if (dataAttr !== null) {
		const result = compileExpr(dataAttr)({})
		const scope = reactive(
			typeof result === 'object' && result !== null ? result : {},
		)
		state.scope = scope
		// hc-persist: hydrate scope keys from localStorage BEFORE init runs,
		// so init() observes restored values.
		processPersist(el, scope)
		// Run init() if defined on the scope
		if (typeof (scope as any).init === 'function') {
			try {
				;(scope as any).init.call(scope)
			} catch (err) {
				console.error('[hyperchi] init() error', err)
			}
		}
	}

	const scope = findScope(el)
	if (!scope) {
		// No scope above: of the directives, only hc-cloak applies.
		if (el.hasAttribute('hc-cloak')) el.removeAttribute('hc-cloak')
		return
	}

	processInit(el, scope)
	processTransition(el)
	processShow(el, scope)
	processText(el, scope)
	processHtml(el, scope)
	processRef(el, scope)
	processEffect(el, scope)
	processModel(el, scope)
	processBindAndOn(el, scope)

	if (el.hasAttribute('hc-cloak')) el.removeAttribute('hc-cloak')
}

// ============================================================================
// Directive implementations
// ============================================================================

function processInit(el: Element, scope: Scope): void {
	const v = el.getAttribute('hc-init')
	if (v === null) return
	compileStmt(v)(scope, new Event('hc-init'))
}

function processShow(el: Element, scope: Scope): void {
	const v = el.getAttribute('hc-show')
	if (v === null) return
	const expr = compileExpr(v)
	const html = el as HTMLElement
	const previousDisplay = html.style.display
	const state = getState(el)
	let lastVisible: boolean | undefined
	const e = effect(() => {
		const visible = !!expr(scope)
		const transition = state.transition
		if (visible === lastVisible) return
		// First run: set the state without animating.
		if (lastVisible === undefined || !transition) {
			html.style.display = visible ? previousDisplay : 'none'
		} else if (visible) {
			html.style.display = previousDisplay
			runTransitionClass(html, transition.enter)
		} else {
			runTransitionClass(html, transition.leave, () => {
				html.style.display = 'none'
			})
		}
		lastVisible = visible
	})
	state.effects.push(e)
}

/**
 * Add a class for the duration of one transition/animation cycle, then remove
 * it. If `after` is provided, run it after the cleanup (used for leave to
 * defer setting display:none until the leave animation completes).
 */
function runTransitionClass(
	el: HTMLElement,
	className: string | undefined,
	after?: () => void,
): void {
	if (!className) {
		after?.()
		return
	}
	el.classList.add(className)
	let done = false
	const cleanup = () => {
		if (done) return
		done = true
		el.classList.remove(className)
		el.removeEventListener('transitionend', cleanup)
		el.removeEventListener('animationend', cleanup)
		after?.()
	}
	el.addEventListener('transitionend', cleanup, { once: true })
	el.addEventListener('animationend', cleanup, { once: true })
	// Fallback: if the transition never fires (e.g. no CSS rule), clean up
	// after a generous timeout so display:none still gets applied.
	setTimeout(cleanup, 1500)
}

function processTransition(el: Element): void {
	const v = el.getAttribute('hc-transition')
	if (v === null) return
	const config: { enter?: string; leave?: string } = {}
	for (const part of v.split('|').map((s) => s.trim())) {
		const idx = part.indexOf(':')
		if (idx < 0) continue
		const key = part.slice(0, idx).trim()
		const cls = part.slice(idx + 1).trim()
		if (key === 'enter') config.enter = cls
		else if (key === 'leave') config.leave = cls
	}
	getState(el).transition = config
}

function processText(el: Element, scope: Scope): void {
	const v = el.getAttribute('hc-text')
	if (v === null) return
	const expr = compileExpr(v)
	const e = effect(() => {
		el.textContent = stringify(expr(scope))
	})
	getState(el).effects.push(e)
}

function processHtml(el: Element, scope: Scope): void {
	const v = el.getAttribute('hc-html')
	if (v === null) return
	const expr = compileExpr(v)
	const e = effect(() => {
		el.innerHTML = config.sanitizeHTML(stringify(expr(scope)))
	})
	getState(el).effects.push(e)
}

function processRef(el: Element, scope: Scope): void {
	const v = el.getAttribute('hc-ref')
	if (v === null) return
	if (!scope.$refs) (scope as any).$refs = {}
	;(scope as any).$refs[v] = el
}

function processEffect(el: Element, scope: Scope): void {
	const v = el.getAttribute('hc-effect')
	if (v === null) return
	const stmt = compileStmt(v)
	const e = effect(() => {
		stmt(scope, new Event('hc-effect'))
	})
	getState(el).effects.push(e)
}

function processBindAndOn(el: Element, scope: Scope): void {
	const state = getState(el)
	for (const attr of Array.from(el.attributes)) {
		const name = attr.name

		// hc-bind:foo  /  :foo
		let bindKey: string | null = null
		if (name.startsWith('hc-bind:')) bindKey = name.slice('hc-bind:'.length)
		else if (name.startsWith(':') && name.length > 1) bindKey = name.slice(1)

		if (bindKey !== null) {
			const key = bindKey
			const expr = compileExpr(attr.value)
			const e = effect(() => {
				applyBinding(el, key, expr(scope))
			})
			state.effects.push(e)
			continue
		}

		// hc-on:event[.modifier...]  /  @event[.modifier...]
		let eventPart: string | null = null
		if (name.startsWith('hc-on:')) eventPart = name.slice('hc-on:'.length)
		else if (name.startsWith('@') && name.length > 1) eventPart = name.slice(1)

		if (eventPart !== null) {
			const [eventName, ...modifiers] = eventPart.split('.')
			const stmt = compileStmt(attr.value)
			const isSelf = modifiers.includes('self')
			const doPrevent = modifiers.includes('prevent')
			const doStop = modifiers.includes('stop')
			const isOnce = modifiers.includes('once')

			const handler = (e: Event) => {
				if (isSelf && e.target !== e.currentTarget) return
				if (doPrevent) e.preventDefault()
				if (doStop) e.stopPropagation()
				stmt(scope, e)
			}
			el.addEventListener(eventName, handler, isOnce ? { once: true } : undefined)
			state.removers.push(() => el.removeEventListener(eventName, handler))
		}
	}
}

/**
 * hc-if: replace the element with a comment placeholder when the expression is
 * falsy; restore (and re-walk) when it becomes truthy. Children are torn down
 * on detach so their effects/listeners are released.
 */
function processIf(el: Element): void {
	const v = el.getAttribute('hc-if')
	if (v === null) return
	// hc-if introduces no scope, so it evaluates in its parent's.
	const parent = el.parentElement
	if (!parent) return
	const parentScope = findScope(parent)
	if (!parentScope) {
		// No scope to evaluate in: leave the element visible and walk it.
		el.removeAttribute('hc-if')
		walk(el)
		return
	}
	const expr = compileExpr(v)
	const placeholder = document.createComment(' hc-if ')
	parent.insertBefore(placeholder, el)
	let isAttached = true
	// Strip the attribute so the recursive walk doesn't re-trigger hc-if.
	el.removeAttribute('hc-if')

	const e = effect(() => {
		const shouldShow = !!expr(parentScope)
		if (shouldShow && !isAttached) {
			placeholder.parentNode?.insertBefore(el, placeholder.nextSibling)
			isAttached = true
			walk(el)
		} else if (!shouldShow && isAttached) {
			teardown(el)
			el.remove()
			isAttached = false
		} else if (shouldShow && isAttached) {
			// Initial run while still attached: walk for descendant directives.
			walk(el)
		}
	})
	// Track effect on the placeholder so it survives even when el is removed.
	getState(placeholder as unknown as Element).effects.push(e)
}

/**
 * hc-for: clone the template element once per item in the iterable.
 *
 * Syntax: "item in items" or "item, idx in items"
 *
 * Each clone gets its own iter-scope ({item, idx}) which combines with the
 * parent scope chain via mergedScope, so descendants can reference both.
 */
function processFor(el: Element): void {
	const v = el.getAttribute('hc-for')
	if (v === null) return
	const m = v.match(/^\s*(\w+)(?:\s*,\s*(\w+))?\s+in\s+(.+)$/)
	if (!m) {
		console.error(`[hyperchi] hc-for syntax: "item in items"; got ${v}`)
		return
	}
	const itemVar = m[1]
	const indexVar = m[2]
	const listSrc = m[3]
	const parent = el.parentElement
	if (!parent) return
	const parentScope = findScope(parent)
	if (!parentScope) return

	const expr = compileExpr(listSrc)
	const marker = document.createComment(' hc-for ')
	parent.insertBefore(marker, el)
	el.remove()
	// Strip hc-for from the template so cloned elements don't re-trigger.
	const template = el.cloneNode(true) as Element
	template.removeAttribute('hc-for')

	let cloned: Element[] = []

	const e = effect(() => {
		// Tear down previous clones
		for (const c of cloned) {
			teardown(c)
			c.remove()
		}
		cloned = []

		const items = expr(parentScope)
		if (items == null) return
		// Accept arrays and any iterable
		const list: any[] = Array.isArray(items)
			? items
			: typeof (items as any)[Symbol.iterator] === 'function'
			  ? Array.from(items as any)
			  : []

		let prev: Node = marker
		list.forEach((item, idx) => {
			const clone = template.cloneNode(true) as Element
			const iterScope: Scope = reactive(
				indexVar ? { [itemVar]: item, [indexVar]: idx } : { [itemVar]: item },
			)
			getState(clone).scope = iterScope
			parent.insertBefore(clone, prev.nextSibling)
			walk(clone)
			cloned.push(clone)
			prev = clone
		})
	})
	// Park the effect on the marker so it survives template/clone churn.
	getState(marker as unknown as Element).effects.push(e)
}

/**
 * hc-model: two-way bind a form element's value to scope[key].
 *
 * Supported elements/types: text-style inputs, textarea, select, checkbox, radio.
 */
function processModel(el: Element, scope: Scope): void {
	const v = el.getAttribute('hc-model')
	if (v === null) return
	const key = v.trim()
	const tag = el.tagName
	const type = (el as HTMLInputElement).type
	const isCheckbox = tag === 'INPUT' && type === 'checkbox'
	const isRadio = tag === 'INPUT' && type === 'radio'
	const eventName =
		isCheckbox || isRadio || tag === 'SELECT' ? 'change' : 'input'

	// Initial direction: if scope already has a value, push it to the element;
	// otherwise pull the element's current value into the scope.
	if (scope[key] === undefined) {
		setScopeFromEl(scope, key, el, isCheckbox, isRadio)
	}

	// Effect: scope → element
	const e = effect(() => {
		const value = scope[key]
		setElValue(el, value, isCheckbox, isRadio)
	})
	getState(el).effects.push(e)

	// Listener: element → scope
	const handler = () => setScopeFromEl(scope, key, el, isCheckbox, isRadio)
	el.addEventListener(eventName, handler)
	getState(el).removers.push(() =>
		el.removeEventListener(eventName, handler),
	)
}

function setElValue(
	el: Element,
	value: any,
	isCheckbox: boolean,
	isRadio: boolean,
): void {
	const input = el as HTMLInputElement
	if (isCheckbox) {
		input.checked = !!value
	} else if (isRadio) {
		input.checked = String(value) === input.value
	} else {
		const next = value == null ? '' : String(value)
		if ((input as any).value !== next) (input as any).value = next
	}
}

function setScopeFromEl(
	scope: Scope,
	key: string,
	el: Element,
	isCheckbox: boolean,
	isRadio: boolean,
): void {
	const input = el as HTMLInputElement
	if (isCheckbox) {
		scope[key] = input.checked
	} else if (isRadio) {
		if (input.checked) scope[key] = input.value
	} else {
		scope[key] = (el as HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement).value
	}
}

/**
 * hc-persist: hydrate scope[key] from localStorage on init, and write back on
 * every change. Format: "key" (uses localStorage key "_hc_<key>") or
 * "key:lsKey". Multiple keys can be comma-separated.
 */
function processPersist(el: Element, scope: Scope): void {
	const v = el.getAttribute('hc-persist')
	if (v === null) return
	for (const entry of v.split(',').map((s) => s.trim()).filter(Boolean)) {
		const [key, customLs] = entry.split(':').map((s) => s.trim())
		const lsKey = customLs || `_hc_${key}`
		// Hydrate before any reactive subscription so init() sees the value.
		const stored = localStorage.getItem(lsKey)
		if (stored !== null) {
			try {
				scope[key] = JSON.parse(stored)
			} catch {
				scope[key] = stored
			}
		}
		// Mirror future changes back to localStorage.
		const e = effect(() => {
			const value = scope[key]
			if (value === undefined) return
			try {
				localStorage.setItem(lsKey, JSON.stringify(value))
			} catch (err) {
				console.error(`[hyperchi] hc-persist write failed for ${lsKey}`, err)
			}
		})
		getState(el).effects.push(e)
	}
}

// ============================================================================
// Binding helpers
// ============================================================================

function applyBinding(el: Element, key: string, value: any): void {
	if (key === 'class') {
		applyClass(el, value)
	} else if (key === 'style') {
		applyStyle(el, value)
	} else {
		// Boolean attribute semantics
		if (value === false || value == null) {
			el.removeAttribute(key)
		} else if (value === true) {
			el.setAttribute(key, '')
		} else {
			el.setAttribute(key, String(value))
		}
	}
}

function applyClass(el: Element, value: any): void {
	const state = getState(el)
	if (!state.boundClasses) state.boundClasses = new Set()
	const next = new Set<string>(normalizeClass(value))
	for (const c of state.boundClasses) {
		if (!next.has(c)) el.classList.remove(c)
	}
	for (const c of next) {
		if (!state.boundClasses.has(c)) el.classList.add(c)
	}
	state.boundClasses = next
}

function normalizeClass(value: any): string[] {
	if (typeof value === 'string') return value.split(/\s+/).filter(Boolean)
	if (Array.isArray(value)) return value.flatMap(normalizeClass)
	if (value && typeof value === 'object') {
		// Object keys may themselves be space-separated multi-class strings
		// (a common Tailwind pattern), so split each truthy key.
		return Object.entries(value)
			.filter(([, v]) => !!v)
			.flatMap(([k]) => k.split(/\s+/).filter(Boolean))
	}
	return []
}

function applyStyle(el: Element, value: any): void {
	const state = getState(el)
	if (!state.boundStyles) state.boundStyles = new Set()
	const next = new Set<string>()
	const html = el as HTMLElement
	if (value && typeof value === 'object') {
		for (const [k, v] of Object.entries(value)) {
			next.add(k)
			html.style.setProperty(camelToKebab(k), v == null ? '' : String(v))
		}
	}
	for (const k of state.boundStyles) {
		if (!next.has(k)) html.style.removeProperty(camelToKebab(k))
	}
	state.boundStyles = next
}

function camelToKebab(s: string): string {
	return s.replace(/[A-Z]/g, (c) => '-' + c.toLowerCase())
}

function stringify(value: any): string {
	if (value == null) return ''
	if (typeof value === 'object') {
		try {
			return JSON.stringify(value)
		} catch {
			return String(value)
		}
	}
	return String(value)
}

// ============================================================================
// Lifecycle: teardown on element removal
// ============================================================================

function teardown(el: Element): void {
	const s = states.get(el)
	if (s) {
		for (const e of s.effects) stop(e)
		for (const r of s.removers) r()
		states.delete(el)
	}
	for (const child of Array.from(el.children)) teardown(child)
}

// ============================================================================
// MutationObserver + HTMX hooks
// ============================================================================

function observe(root: Element): void {
	const observer = new MutationObserver((records) => {
		for (const r of records) {
			r.addedNodes.forEach((n) => {
				if (n.nodeType === 1) walk(n as Element)
			})
			r.removedNodes.forEach((n) => {
				if (n.nodeType === 1) teardown(n as Element)
			})
		}
	})
	observer.observe(root, { childList: true, subtree: true })
}

function hookHtmx(): void {
	// HTMX swaps DOM nodes in via outerHTML/innerHTML. MutationObserver above
	// catches those, but htmx:after:swap gives us a single re-walk per swap.
	// htmx 4 dispatches after:swap on the source element with detail.ctx.target
	// set to the hx-target *selector string* (not an element), so resolve it to
	// the swap destination; fall back to the event's source element.
	document.body.addEventListener('htmx:after:swap', (e: Event) => {
		const root = swapRoot(e)
		if (root) walk(root)
	})
}

// swapRoot resolves the element that received swapped content from an htmx 4
// swap event. detail.ctx.target may be a selector string or an element; the
// event target is the source element as a last resort.
function swapRoot(e: Event): Element | null {
	const target = (e as CustomEvent).detail?.ctx?.target
	if (target instanceof Element) return target
	if (typeof target === 'string') {
		const found = document.querySelector(target)
		if (found) return found
	}
	return e.target instanceof Element ? e.target : null
}
