"""Run against the showcase: SHOWCASE_URL=http://localhost:9999 python3 scripts/integrations_smoke.py.
Requires playwright and a local Chrome/Chromium. Screenshots go to tmp/screenshots.
"""
import os
import shutil
from pathlib import Path
from playwright.sync_api import sync_playwright, expect

BASE = os.environ.get('SHOWCASE_URL', 'http://localhost:9999').rstrip('/')
CHROME = os.environ.get('CHROME_PATH') or shutil.which('google-chrome') or shutil.which('chromium')
Path('tmp/screenshots').mkdir(parents=True, exist_ok=True)
with sync_playwright() as p:
    browser = p.chromium.launch(executable_path=CHROME, headless=True, args=['--no-sandbox'])
    context = browser.new_context(viewport={'width': 1440, 'height': 1000}, reduced_motion='reduce')
    page = context.new_page()
    errors = []
    page.on('pageerror', lambda error: errors.append(str(error)))
    for name in ['templ', 'jet', 'alpine']:
        response = page.goto(f'{BASE}/examples/{name}')
        assert response.status == 200
        expect(page.locator('h1')).to_have_count(1)
        assert page.locator('main section').count() >= 7
        assert page.evaluate('document.documentElement.scrollWidth <= innerWidth')
        assert page.locator('img').evaluate_all('(images) => images.every(i => i.complete && i.naturalWidth > 0)')
        for link in page.locator('a[href^="#"]').all():
            assert page.locator(link.get_attribute('href')).count() == 1
        page.screenshot(path=f'tmp/screenshots/{name}-desktop.png', full_page=True)
    page.goto(BASE + '/examples/templ')
    for scope, price in [('identity', '$4,000'), ('complete', '$9,000'), ('launch', '$6,000')]:
        page.select_option('#scope', scope)
        page.get_by_role('button', name='Calculate estimate').click()
        expect(page.locator('#estimate-result')).to_contain_text(price)
        assert page.url == BASE + '/examples/templ', 'htmx should update without navigation'
    page.goto(BASE + '/examples/jet')
    for billing, price in [('annual', '$32.50'), ('monthly', '$39')]:
        page.select_option('#billing', billing)
        page.get_by_role('button', name='Update pricing').click()
        expect(page.locator('#pricing-result')).to_contain_text(price)
        assert page.url == BASE + '/examples/jet'
    page.goto(BASE + '/examples/alpine')
    for terrain, count in [('coast', 1), ('mountain', 2), ('forest', 1), ('all', 4)]:
        page.select_option('#terrain', terrain)
        page.get_by_role('button', name='Find trips').click()
        expect(page.locator('.trip:visible')).to_have_count(count)
        expect(page.locator('#trips')).to_contain_text(f'{count} trip')
    # Loading, empty, failure/retry, and escaping are browser-facing contracts.
    page.route('**/examples/alpine/trips?*', lambda route: route.fulfill(json={'trips': [], 'count': 0}))
    page.get_by_role('button', name='Find trips').click()
    expect(page.get_by_text('No trips for this landscape yet.')).to_be_visible()
    page.unroute('**/examples/alpine/trips?*')
    page.route('**/examples/alpine/trips?*', lambda route: route.fulfill(status=503, body='Unavailable'))
    page.get_by_role('button', name='Find trips').click()
    expect(page.locator('#trips [role=alert]')).to_be_visible()
    page.unroute('**/examples/alpine/trips?*')
    page.get_by_role('button', name='Try again').click()
    expect(page.locator('.trip:visible')).to_have_count(4)
    payload = '<img src=x onerror=alert(1)>'
    page.route('**/examples/alpine/trips?*', lambda route: route.fulfill(json={'trips': [{'name': payload, 'terrain': 'coast', 'days': 3, 'price': 420, 'description': payload}], 'count': 1}))
    page.get_by_role('button', name='Find trips').click()
    expect(page.locator('.trip:visible')).to_have_count(1)
    expect(page.locator('.trip:visible h3')).to_have_text(payload)
    expect(page.locator('.trip img')).to_have_count(0)
    page.unroute('**/examples/alpine/trips?*')
    for name in ['templ', 'jet']:
        page.goto(f'{BASE}/examples/{name}')
        page.route(f'**/examples/{name}/*', lambda route: route.fulfill(status=503, body='Unavailable'))
        page.locator('button[type=submit]').click()
        expect(page.locator('.request-error')).to_be_visible()
        page.unroute(f'**/examples/{name}/*')
        page.locator('button[type=submit]').click()
        expect(page.locator('.request-error')).to_be_hidden()
    for width in [390, 768]:
        page.set_viewport_size({'width': width, 'height': 844})
        for name in ['templ', 'jet', 'alpine']:
            page.goto(f'{BASE}/examples/{name}')
            assert page.evaluate('document.documentElement.scrollWidth <= innerWidth'), (name, width)
            page.screenshot(path=f'tmp/screenshots/{name}-{width}.png', full_page=True)
    for topic in ['helpers', 'filters', 'integration-manager', 'integrations', 'templ', 'jet', 'alpine', 'data']:
        assert page.goto(BASE + '/docs/' + topic).status == 200
        expect(page.locator('.docs-sidebar a[aria-current]')).to_have_count(1)
        assert page.evaluate('document.documentElement.scrollWidth <= innerWidth'), topic
    page.goto(BASE + '/docs/filters')
    expect(page.locator('.docs-filter-output')).to_have_count(10)
    expect(page.locator('.filter-reference')).to_have_count(8)
    page.locator('.filter-reference summary').first.click()
    expect(page.locator('#filter-truncate')).to_be_visible()
    for width in [1440, 390]:
        page.set_viewport_size({'width': width, 'height': 1000})
        assert page.evaluate('document.documentElement.scrollWidth <= innerWidth'), 'filters overflow'
    page.locator('#docs-search').fill('filters')
    expect(page.locator('.docs-sidebar nav a:visible')).to_have_count(1)
    expect(page.locator('.docs-empty')).to_be_hidden()
    page.locator('#docs-search').fill('alpine')
    expect(page.locator('.docs-sidebar nav a:visible')).to_have_count(1)
    page.locator('#docs-search').fill('not-a-topic')
    expect(page.locator('.docs-empty')).to_be_visible()
    plain = browser.new_context(java_script_enabled=False, viewport={'width': 390, 'height': 844})
    fallback = plain.new_page()
    fallback.goto(BASE + '/examples/templ')
    fallback.select_option('#scope', 'identity')
    fallback.get_by_role('button', name='Calculate estimate').click()
    expect(fallback.locator('#estimate-result')).to_contain_text('$4,000')
    expect(fallback.locator('#scope')).to_have_value('identity')
    fallback.goto(BASE + '/examples/jet')
    fallback.select_option('#billing', 'annual')
    fallback.get_by_role('button', name='Update pricing').click()
    expect(fallback.locator('#pricing-result')).to_contain_text('$32.50')
    fallback.goto(BASE + '/examples/alpine')
    expect(fallback.locator('.trip:visible')).to_have_count(4)
    assert not errors, errors
    browser.close()
print('Integration pages, interactions, error states, docs, mobile layouts, and no-JS fallbacks passed.')
