"""Adversarial browser checks against the running showcase; uses Playwright."""
import os
import shutil
from playwright.sync_api import sync_playwright

base = os.environ.get('SHOWCASE_URL', 'http://localhost:9999').rstrip('/')
chrome = os.environ.get('CHROME_PATH') or shutil.which('google-chrome') or shutil.which('chromium')
with sync_playwright() as p:
    browser = p.chromium.launch(executable_path=chrome, headless=True, args=['--no-sandbox'])
    page = browser.new_page(reduced_motion='reduce')
    page.goto(base)
    close_code = page.evaluate('''async () => {
      const socket = new WebSocket(location.origin.replace('http', 'ws') + '/ws?rooms=activity');
      return await new Promise((resolve, reject) => {
        const timer = setTimeout(() => reject(new Error('socket not rejected')), 5000);
        socket.onopen = () => socket.send(JSON.stringify({room:'activity',content:'<b id="injected">attack</b>',target:'body',swap:'innerHTML'}));
        socket.onclose = event => { clearTimeout(timer); resolve(event.code); };
      });
    }''')
    assert close_code == 1008, close_code
    assert page.locator('#injected').count() == 0
    response = page.request.get(base + '/demo/echo', headers={'HX-Request':'true','HX-Target':'<img src=x onerror=alert(1)>'})
    assert '<img src=x' not in response.text()
    assert '&lt;img' in response.text()
    response = page.request.post(base + '/activity/send', headers={'Origin':'https://evil.example'}, form={'message':'cross-site'})
    assert response.status == 403, response.status
    response = page.request.get(base + '/ws?rooms=private')
    assert response.status == 400
    for path in ['/static/', '/static/.env', '/__hyperchi/hot-reload', '/htmx/extensions']:
        assert page.request.get(base + path).status == 404, path
    page.evaluate('''() => {
      const feed = document.getElementById('activity-log');
      for (let i=0;i<100;i++) feed.appendChild(document.createElement('div'));
    }''')
    page.wait_for_function("document.getElementById('activity-log').children.length <= 50")
    browser.close()
print('PASS: WS injection rejected, echo escaped, cross-origin POST denied, rooms restricted, private paths hidden, feed bounded')
