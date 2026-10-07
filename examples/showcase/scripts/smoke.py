"""Browser smoke checks against a running showcase (default port 9999).
Run from examples/showcase with: python scripts/smoke.py
Requires the Python playwright package and Chromium/Chrome.
Set SHOWCASE_URL and CHROME_PATH to override local defaults.
"""
from playwright.sync_api import sync_playwright, expect
import json
from pathlib import Path
import os
import shutil
BASE=os.environ.get('SHOWCASE_URL', 'http://localhost:9999').rstrip('/')
CHROME=os.environ.get('CHROME_PATH') or shutil.which('google-chrome') or shutil.which('chromium')
Path('tmp/screenshots').mkdir(parents=True, exist_ok=True)
with sync_playwright() as p:
 b=p.chromium.launch(executable_path=CHROME,headless=True,args=['--no-sandbox','--enable-unsafe-swiftshader'])
 context=b.new_context(viewport={'width':1440,'height':1000},color_scheme='dark',permissions=['clipboard-read','clipboard-write'])
 page=context.new_page()
 errors=[]
 page.on('pageerror', lambda e:errors.append(str(e)))
 page.on('console', lambda m:errors.append(m.text) if m.type=='error' else None)
 page.goto(BASE,wait_until='domcontentloaded')
 page.wait_for_timeout(1200)
 expect(page.locator('.sculpture')).to_have_class('sculpture ready')
 canvas=page.locator('#sculpture canvas')
 bounds=canvas.bounding_box()
 assert bounds['x']==0 and bounds['width']==1440
 first=canvas.screenshot();page.wait_for_timeout(500)
 assert first!=canvas.screenshot(), 'Network packets should animate'
 page.get_by_role('button',name='Pause motion',exact=True).click()
 still=canvas.screenshot();page.wait_for_timeout(500)
 assert still==canvas.screenshot(), 'Pause must freeze the network'
 page.get_by_role('button',name='Play motion',exact=True).click()
 page.locator('#send-request').click()
 expect(page.locator('#echo-out')).to_contain_text('negotiated:')
 page.get_by_role('button',name='Try JSON').click()
 expect(page.locator('#echo-out')).to_contain_text('"htmx":true')
 expect(page.locator('#proof .metric').last).to_contain_text('live')
 page.locator('#realtime summary').click()
 page.locator('#message').fill('HyperChi browser smoke')
 page.locator('#realtime button').click()
 expect(page.locator('#activity-log')).to_contain_text('HyperChi browser smoke')
 page.locator('#changelog summary').click()
 page.locator('#search').fill('upload')
 expect(page.locator('#search-results')).to_contain_text('Upload')
 page.locator('#upload summary').click()
 page.locator('input[type=file]').set_input_files({'name':'example.txt','mimeType':'text/plain','buffer':b'Hello HyperChi'})
 page.get_by_role('button',name='Validate file').click()
 expect(page.locator('#upload-out')).not_to_be_empty()
 print('Upload response:',page.locator('#upload-out').inner_text())
 page.locator('#waitlist summary').click()
 page.locator('input[name=email]').fill('demo@example.com')
 page.locator('#wizard button[type=submit]').click()
 expect(page.locator('select[name=role]')).to_be_visible()
 page.locator('select[name=role]').select_option('backend')
 page.locator('#wizard button[type=submit]').click()
 expect(page.locator('#wizard')).to_contain_text('Confirm')
 page.locator('.install-command button').click()
 expect(page.locator('.install-command button')).to_have_text('Copied')
 assert page.evaluate('navigator.clipboard.readText()')=='go get github.com/regiellis/hyperchi'
 page.locator('.theme-control').click()
 assert page.evaluate('document.documentElement.dataset.theme')=='light'
 page.screenshot(path='tmp/screenshots/home-light.png',full_page=True)
 for path,title in [('/docs','Getting started'),('/docs/routing','Routing & responses'),('/docs/templates','Templates'),('/docs/realtime','Realtime'),('/docs/directives','Directives')]:
  res=page.goto(BASE+path,wait_until='domcontentloaded')
  assert res.status==200
  expect(page.locator('h1')).to_have_text(title)
  expect(page.locator('.docs-sidebar a[aria-current]')).to_have_count(1)
  assert page.locator('canvas').count()==0
  assert page.evaluate('document.documentElement.scrollWidth <= innerWidth')
 page.get_by_role('button',name='Add one').click()
 expect(page.locator('output')).to_have_text('1')
 page.get_by_role('button',name='Reset',exact=True).click()
 expect(page.locator('output')).to_have_text('0')
 page.locator('#docs-search').fill('xyz-no-topic')
 expect(page.locator('.docs-empty')).to_be_visible()
 page.locator('#docs-search').fill('routing')
 expect(page.locator('.docs-sidebar nav a:visible')).to_have_count(1)
 page.locator('#docs-search').fill('')
 page.screenshot(path='tmp/screenshots/docs-light.png',full_page=True)
 page.locator('.theme-control').click()
 page.wait_for_timeout(300)
 page.screenshot(path='tmp/screenshots/docs-dark.png',full_page=True)
 for width in [390,768,1024]:
  page.set_viewport_size({'width':width,'height':844})
  page.goto(BASE,wait_until='domcontentloaded')
  page.wait_for_timeout(900)
  assert page.evaluate('document.documentElement.scrollWidth <= innerWidth'), f'Overflow at {width}'
  bounds=page.locator('#sculpture').bounding_box()
  assert bounds['x']==0 and bounds['width']==width
  page.screenshot(path=f'tmp/screenshots/home-{width}.png',full_page=True)
  if width==390:
   assert page.locator('canvas').count()==0
   page.get_by_role('button',name='Play motion',exact=True).click()
   expect(page.locator('.sculpture')).to_have_class('sculpture ready')
   page.get_by_role('button',name='Pause motion',exact=True).click()
   page.get_by_role('button',name='Menu',exact=True).click()
   expect(page.locator('#mobile-nav')).to_be_visible()
   page.locator('#mobile-nav').get_by_role('link',name='Documentation').click()
   expect(page.locator('h1')).to_have_text('Getting started')
   assert page.evaluate('document.documentElement.scrollWidth <= innerWidth')
   page.screenshot(path='tmp/screenshots/docs-mobile.png',full_page=True)
 reduced=b.new_context(viewport={'width':1280,'height':900},reduced_motion='reduce',color_scheme='dark')
 rp=reduced.new_page();rp.goto(BASE,wait_until='domcontentloaded');rp.wait_for_timeout(900)
 expect(rp.locator('.motion-control')).to_have_text('Play motion')
 assert rp.locator('canvas').count()==0
 assert rp.locator('h1').is_visible()
 nojs=b.new_context(java_script_enabled=False,viewport={'width':390,'height':844})
 np=nojs.new_page();np.goto(BASE,wait_until='domcontentloaded')
 assert np.locator('h1').is_visible()
 assert np.locator('.sculpture-poster').evaluate('(el)=>el.complete && el.naturalWidth>0')
 print('Browser errors:',errors)
 assert not errors
 print('PASS: routes, htmx HTML/JSON, SSE, WebSocket, search, upload, wizard, clipboard, docs filter, directives, themes, responsive layout, reduced motion, no-JS artwork')
 b.close()
