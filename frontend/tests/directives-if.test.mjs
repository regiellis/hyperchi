// hc-if regression tests on a fake DOM that supports what hc-if needs:
// comment placeholders, insertBefore, remove, textContent, and listeners.
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { build } from 'esbuild'

const { outputFiles } = await build({
	entryPoints: [new URL('../assets/scripts/hyperchi-directives.ts', import.meta.url).pathname],
	bundle: true,
	format: 'esm',
	platform: 'neutral',
	target: 'es2020',
	write: false,
})
const HyperChi = await import(
	'data:text/javascript;base64,' + Buffer.from(outputFiles[0].text).toString('base64')
)

class FakeNode {
	constructor() {
		this.parentNode = null
		this.nodeType = 8
	}
	get parentElement() {
		return this.parentNode
	}
	get nextSibling() {
		const nodes = this.parentNode?.nodes ?? []
		return nodes[nodes.indexOf(this) + 1] ?? null
	}
	remove() {
		if (!this.parentNode) return
		const nodes = this.parentNode.nodes
		nodes.splice(nodes.indexOf(this), 1)
		this.parentNode = null
	}
}

class FakeElement extends FakeNode {
	constructor(attrs, children = []) {
		super()
		this.nodeType = 1
		this.attrs = new Map(Object.entries(attrs))
		this.nodes = []
		this.listeners = new Map()
		this.textContent = ''
		for (const c of children) this.insertBefore(c, null)
	}
	get children() {
		return this.nodes.filter((n) => n instanceof FakeElement)
	}
	insertBefore(node, ref) {
		node.remove()
		const at = ref ? this.nodes.indexOf(ref) : -1
		if (at < 0) this.nodes.push(node)
		else this.nodes.splice(at, 0, node)
		node.parentNode = this
		return node
	}
	hasAttribute(name) {
		return this.attrs.has(name)
	}
	getAttribute(name) {
		return this.attrs.has(name) ? this.attrs.get(name) : null
	}
	removeAttribute(name) {
		this.attrs.delete(name)
	}
	get attributes() {
		return Array.from(this.attrs, ([name, value]) => ({ name, value }))
	}
	addEventListener(name, fn) {
		this.listeners.set(name, fn)
	}
	removeEventListener(name) {
		this.listeners.delete(name)
	}
	click() {
		this.listeners.get('click')?.({ target: this, currentTarget: this })
	}
}

globalThis.document = { createComment: () => new FakeNode() }

// <div hc-data="{ name: '' }">
//   <button hc-on:click="name = 'Mai'"></button>
//   <p hc-if="name">Hello <span hc-text="name"></span></p>
// </div>
function mount() {
	const span = new FakeElement({ 'hc-text': 'name' })
	const p = new FakeElement({ 'hc-if': 'name' }, [span])
	const button = new FakeElement({ 'hc-on:click': "name = 'Mai'" })
	const root = new FakeElement({ 'hc-data': "{ name: '' }" }, [button, p])
	HyperChi.refresh(root)
	return { root, button, p, span }
}

test('hc-if that starts false binds its descendants when it turns true', () => {
	const { root, button, p, span } = mount()
	assert.ok(!root.nodes.includes(p), 'hidden while name is empty')
	button.click()
	assert.ok(root.nodes.includes(p), 'shown after name is set')
	assert.equal(span.textContent, 'Mai')
})

test('hc-if that starts true keeps its descendants bound', () => {
	const span = new FakeElement({ 'hc-text': 'name' })
	const p = new FakeElement({ 'hc-if': 'name' }, [span])
	const root = new FakeElement({ 'hc-data': "{ name: 'Kobe' }" }, [p])
	HyperChi.refresh(root)
	assert.ok(root.nodes.includes(p))
	assert.equal(span.textContent, 'Kobe')
})
