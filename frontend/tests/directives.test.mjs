// Tests for hyperchi-directives.ts. Run with `npm test` (node:test, no DOM):
// the module is bundled in memory with esbuild and driven through refresh()
// on minimal fake elements.
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

// FakeElement implements the slice of Element that refresh() touches for
// hc-data and hc-html.
class FakeElement {
	constructor(attrs, children = []) {
		this.attrs = new Map(Object.entries(attrs))
		this.children = children
		this.parentElement = null
		this.innerHTML = ''
		for (const c of children) c.parentElement = this
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
}

const payload = '<b>hi</b><img src=x onerror="alert(1)">'

function renderHTML() {
	const target = new FakeElement({ 'hc-html': 'markup' })
	const root = new FakeElement({ 'hc-data': `{ markup: ${JSON.stringify(payload)} }` }, [target])
	HyperChi.refresh(root)
	return target.innerHTML
}

test('hc-html assigns the expression unchanged by default', () => {
	assert.equal(HyperChi.config.sanitizeHTML(payload), payload)
	assert.equal(renderHTML(), payload)
})

test('hc-html passes markup through config.sanitizeHTML', (t) => {
	const original = HyperChi.config.sanitizeHTML
	t.after(() => {
		HyperChi.config.sanitizeHTML = original
	})
	const seen = []
	HyperChi.config.sanitizeHTML = (html) => {
		seen.push(html)
		return html.replace(/<img[^>]*>/g, '')
	}
	assert.equal(renderHTML(), '<b>hi</b>')
	assert.deepEqual(seen, [payload])
})
