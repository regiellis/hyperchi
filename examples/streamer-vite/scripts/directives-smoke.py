"""
End-to-end smoke test for HyperChi declarative directives, exercised through
the streamer-vite example.

How to run:
    1. In one terminal: cd examples/streamer-vite && go run .
    2. In another:      cd examples/streamer-vite && npm run dev
    3. Then:            python3 examples/streamer-vite/scripts/directives-smoke.py

Requires: pip install playwright && python3 -m playwright install chromium

The script asserts both the Phase 1 binding contract (hc-data, hc-show,
hc-bind/:attr, hc-on/@event, c.Fragment HTMX integration) and the Phase 2
extensions (hc-persist, hc-if, hc-for, hc-model, hc-transition).
"""
from playwright.sync_api import sync_playwright

PASS: list[str] = []
FAIL: list[str] = []
ERRS: list[str] = []


def assert_eq(name, got, want):
    if got == want:
        PASS.append(f"{name}: {got!r}")
    else:
        FAIL.append(f"{name}: got {got!r}, want {want!r}")


def assert_truthy(name, got, hint=""):
    if got:
        PASS.append(f"{name}: {got!r}")
    else:
        FAIL.append(f"{name}: falsy ({got!r}) {hint}".strip())


with sync_playwright() as p:
    browser = p.chromium.launch(headless=True)
    page = browser.new_page()
    page.on(
        "console",
        lambda m: ERRS.append(f"[{m.type}] {m.text}") if m.type == "error" else None,
    )
    page.on("pageerror", lambda e: ERRS.append(f"[pageerror] {e}"))

    page.goto("http://127.0.0.1:8080/")
    page.wait_for_load_state("networkidle")

    # ============================================================
    # Phase 1: streamer-vite app integration
    # ============================================================
    assert_eq(
        "hc-cloak removed from <html>",
        page.evaluate("document.documentElement.hasAttribute('hc-cloak')"),
        False,
    )
    assert_eq(
        "hc-data still on <html>",
        page.evaluate("document.documentElement.hasAttribute('hc-data')"),
        True,
    )
    assert_eq(
        "html.classList contains 'dark' after init()",
        page.evaluate("document.documentElement.classList.contains('dark')"),
        True,
    )

    sun_disp = page.evaluate(
        "getComputedStyle(document.querySelector('svg[hc-show=\"darkMode\"]')).display"
    )
    moon_disp = page.evaluate(
        "getComputedStyle(document.querySelector('svg[hc-show=\"!darkMode\"]')).display"
    )
    assert_truthy("sun visible (darkMode=true)", sun_disp != "none", f"display={sun_disp}")
    assert_eq("moon hidden (display:none)", moon_disp, "none")

    # Toggle theme via @click
    page.evaluate("document.getElementById('theme-toggle').click()")
    page.wait_for_timeout(50)
    assert_eq(
        "after toggle: dark class removed",
        page.evaluate("document.documentElement.classList.contains('dark')"),
        False,
    )
    assert_eq(
        "after toggle: sun display:none",
        page.evaluate(
            "getComputedStyle(document.querySelector('svg[hc-show=\"darkMode\"]')).display"
        ),
        "none",
    )
    assert_truthy(
        "after toggle: moon visible",
        page.evaluate(
            "getComputedStyle(document.querySelector('svg[hc-show=\"!darkMode\"]')).display"
        )
        != "none",
    )
    # Restore for later tests
    page.evaluate("document.getElementById('theme-toggle').click()")
    page.wait_for_timeout(50)

    # First podcast row → @click triggers htmx.ajax → fragment loads
    pid = page.evaluate(
        "document.querySelector('[data-podcast-id]').getAttribute('data-podcast-id')"
    )
    PASS.append(f"first podcast id: {pid}")
    page.evaluate(f"document.querySelector('[data-podcast-id=\"{pid}\"]').click()")
    page.wait_for_function(
        f"document.getElementById('podcast-details-{pid}').innerHTML.trim().length > 0",
        timeout=5000,
    )
    details_html = page.evaluate(
        f"document.getElementById('podcast-details-{pid}').innerHTML"
    )
    assert_truthy("fragment loaded", details_html, f"len={len(details_html)}")
    assert_eq(
        "fragment has no <html> wrapper",
        "<html" in details_html.lower(),
        False,
    )
    assert_eq(
        "fragment has no footer wrapper",
        "lexington themes" in details_html.lower(),
        False,
    )

    # ============================================================
    # Phase 2: hc-persist (real-world streamer-vite usage)
    # ============================================================
    persist_ls = page.evaluate("localStorage.getItem('theme')")
    assert_truthy("hc-persist wrote 'theme' to localStorage", persist_ls)

    page.evaluate("document.getElementById('theme-toggle').click()")
    page.wait_for_timeout(80)
    persist_after = page.evaluate("localStorage.getItem('theme')")
    assert_truthy("hc-persist mirrored toggle", persist_after != persist_ls)
    page.evaluate("document.getElementById('theme-toggle').click()")
    page.wait_for_timeout(50)

    # ============================================================
    # Phase 2: hc-if
    # ============================================================
    page.evaluate(
        """() => {
        const wrap = document.createElement('div');
        wrap.id = 'hcif-fixture';
        wrap.setAttribute('hc-data', '({ open: false })');
        wrap.innerHTML =
            '<div hc-if="open" id="hcif-target">VISIBLE</div>' +
            '<button id="hcif-toggle" @click="open = !open">flip</button>';
        document.body.appendChild(wrap);
    }"""
    )
    page.wait_for_timeout(80)
    assert_eq(
        "hc-if: hidden initially (open=false)",
        page.evaluate("!!document.getElementById('hcif-target')"),
        False,
    )
    page.evaluate("document.getElementById('hcif-toggle').click()")
    page.wait_for_timeout(80)
    assert_eq(
        "hc-if: rendered after flip to true",
        page.evaluate("!!document.getElementById('hcif-target')"),
        True,
    )
    assert_eq(
        "hc-if: target has expected content",
        page.evaluate(
            "document.getElementById('hcif-target')?.textContent ?? null"
        ),
        "VISIBLE",
    )
    page.evaluate("document.getElementById('hcif-toggle').click()")
    page.wait_for_timeout(80)
    assert_eq(
        "hc-if: removed after flip back to false",
        page.evaluate("!!document.getElementById('hcif-target')"),
        False,
    )

    # ============================================================
    # Phase 2: hc-for
    # ============================================================
    page.evaluate(
        """() => {
        const wrap = document.createElement('ul');
        wrap.id = 'hcfor-fixture';
        wrap.setAttribute('hc-data', "({ items: ['a','b','c'] })");
        wrap.innerHTML =
            '<li hc-for="x in items" hc-text="x" class="hcfor-item"></li>' +
            '<button id="hcfor-push" @click="items.push(\\'d\\')">push</button>';
        document.body.appendChild(wrap);
    }"""
    )
    page.wait_for_timeout(80)
    assert_eq(
        "hc-for: rendered 3 clones",
        page.evaluate(
            "document.querySelectorAll('#hcfor-fixture .hcfor-item').length"
        ),
        3,
    )
    assert_eq(
        "hc-for: clone contents",
        page.evaluate(
            "[...document.querySelectorAll('#hcfor-fixture .hcfor-item')].map(e => e.textContent)"
        ),
        ["a", "b", "c"],
    )
    page.evaluate("document.getElementById('hcfor-push').click()")
    page.wait_for_timeout(80)
    assert_eq(
        "hc-for: count after push",
        page.evaluate(
            "document.querySelectorAll('#hcfor-fixture .hcfor-item').length"
        ),
        4,
    )
    assert_eq(
        "hc-for: contents after push",
        page.evaluate(
            "[...document.querySelectorAll('#hcfor-fixture .hcfor-item')].map(e => e.textContent)"
        ),
        ["a", "b", "c", "d"],
    )

    # ============================================================
    # Phase 2: hc-model (text input + checkbox)
    # ============================================================
    page.evaluate(
        """() => {
        const wrap = document.createElement('div');
        wrap.id = 'hcmodel-fixture';
        wrap.setAttribute('hc-data', "({ name: 'init' })");
        wrap.innerHTML =
            '<input id="hcmodel-input" hc-model="name">' +
            '<span id="hcmodel-mirror" hc-text="name"></span>';
        document.body.appendChild(wrap);
    }"""
    )
    page.wait_for_timeout(80)
    assert_eq(
        "hc-model: input initial value from scope",
        page.evaluate("document.getElementById('hcmodel-input').value"),
        "init",
    )
    assert_eq(
        "hc-model: mirror initial text",
        page.evaluate("document.getElementById('hcmodel-mirror').textContent"),
        "init",
    )
    page.evaluate(
        """() => {
        const i = document.getElementById('hcmodel-input');
        i.value = 'typed';
        i.dispatchEvent(new Event('input'));
    }"""
    )
    page.wait_for_timeout(80)
    assert_eq(
        "hc-model: input → scope → mirror",
        page.evaluate("document.getElementById('hcmodel-mirror').textContent"),
        "typed",
    )

    page.evaluate(
        """() => {
        const wrap = document.createElement('div');
        wrap.id = 'hcmodel-cb-fixture';
        wrap.setAttribute('hc-data', "({ agreed: false })");
        wrap.innerHTML =
            '<input type="checkbox" id="hcmodel-cb" hc-model="agreed">' +
            '<span id="hcmodel-cb-mirror" hc-text="agreed"></span>';
        document.body.appendChild(wrap);
    }"""
    )
    page.wait_for_timeout(80)
    assert_eq(
        "hc-model checkbox: initial mirror",
        page.evaluate("document.getElementById('hcmodel-cb-mirror').textContent"),
        "false",
    )
    page.evaluate(
        """() => {
        const cb = document.getElementById('hcmodel-cb');
        cb.checked = true;
        cb.dispatchEvent(new Event('change'));
    }"""
    )
    page.wait_for_timeout(80)
    assert_eq(
        "hc-model checkbox: mirror after toggle",
        page.evaluate("document.getElementById('hcmodel-cb-mirror').textContent"),
        "true",
    )

    # ============================================================
    # Phase 2: hc-transition (CSS class lifecycle around hc-show)
    # ============================================================
    page.evaluate(
        """() => {
        const style = document.createElement('style');
        style.textContent =
            '.fade-enter { transition: opacity 0.05s; opacity: 1; }' +
            '.fade-leave { transition: opacity 0.05s; opacity: 0; }';
        document.head.appendChild(style);

        const wrap = document.createElement('div');
        wrap.id = 'hctr-fixture';
        wrap.setAttribute('hc-data', "({ open: true })");
        wrap.innerHTML =
            '<div id="hctr-target" hc-show="open" hc-transition="enter:fade-enter|leave:fade-leave">CONTENT</div>' +
            '<button id="hctr-hide" @click="open = false">hide</button>';
        document.body.appendChild(wrap);
    }"""
    )
    page.wait_for_timeout(80)
    assert_eq(
        "hc-transition: visible initially (no first-run anim)",
        page.evaluate("document.getElementById('hctr-target').style.display"),
        "",
    )
    page.evaluate("document.getElementById('hctr-hide').click()")
    assert_eq(
        "hc-transition: leave class applied during exit",
        page.evaluate(
            "document.getElementById('hctr-target').classList.contains('fade-leave')"
        ),
        True,
    )
    page.wait_for_timeout(300)
    assert_eq(
        "hc-transition: display:none after leave anim",
        page.evaluate("document.getElementById('hctr-target').style.display"),
        "none",
    )

    browser.close()


print("=== PASS ===")
for line in PASS:
    print(f"  ✓ {line}")
if FAIL:
    print("\n=== FAIL ===")
    for line in FAIL:
        print(f"  ✗ {line}")
if ERRS:
    print("\n=== CONSOLE/PAGE ERRORS ===")
    for line in ERRS:
        print(f"  ! {line}")
print(f"\n{len(PASS)} passed, {len(FAIL)} failed, {len(ERRS)} errors")
exit(1 if (FAIL or ERRS) else 0)
