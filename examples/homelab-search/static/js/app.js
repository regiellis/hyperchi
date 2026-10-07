// Keyboard navigation and the theme toggle, as one HyperChi directives scope
// on <body>. Handlers are hc-on directives, so results swapped in by htmx need
// no rebinding: the keydown listener sits on <body> and reads the current DOM.
const THEME_KEY = "homelab-search-theme";

// Upstream htmx issue, not a HyperChi one: in the htmx 4.0.0 SSE extension
// (hx-sse.js, served unchanged by the framework), cleanup() aborts the
// stream's fetch and then calls reader.cancel() without catching the promise
// it returns. When an htmx swap removes an open hx-sse:connect element (htmx 4
// history restore replaces the body, so pressing Back does it), that promise
// rejects with an AbortError ("BodyStreamBuffer was aborted"). The stream is
// meant to close there, so that one rejection is marked handled. Remove this
// once an htmx release catches it.
window.addEventListener("unhandledrejection", (event) => {
  if (event.reason instanceof DOMException && event.reason.name === "AbortError") {
    event.preventDefault();
  }
});

function isTyping(el) {
  return el instanceof HTMLElement &&
    (el.isContentEditable || el.matches("input, textarea, select"));
}

HyperChi.registerComponent("app", () => ({
  theme: document.documentElement.dataset.theme === "light" ? "light" : "dark",

  toggleTheme() {
    this.theme = this.theme === "light" ? "dark" : "light";
    document.documentElement.dataset.theme = this.theme;
    try {
      localStorage.setItem(THEME_KEY, this.theme);
    } catch {
      /* The choice still holds for this page. */
    }
  },

  results() {
    return Array.from(document.querySelectorAll("#main [data-result]"));
  },

  search() {
    return document.getElementById("q");
  },

  focusResult(list, index) {
    const target = list[Math.max(0, Math.min(index, list.length - 1))];
    if (!target) return;
    target.focus();
    target.scrollIntoView({ block: "nearest" });
  },

  // "/" focuses search. j, k, and the arrow keys move between results. Enter in
  // the search box opens the first result once the results match what was
  // typed. Escape leaves the search box.
  onKey(event) {
    if (event.defaultPrevented || event.ctrlKey || event.metaKey || event.altKey) return;
    const target = event.target;
    const search = this.search();
    const inSearch = target === search;
    const typing = isTyping(target);

    if (event.key === "/" && !typing) {
      event.preventDefault();
      search?.focus();
      search?.select();
      return;
    }
    if (event.key === "Escape" && inSearch) {
      search.blur();
      return;
    }
    if (typing && !inSearch) return; // leave the filters' own keys alone

    const list = this.results();
    if (list.length === 0) return;
    const at = list.indexOf(document.activeElement);
    const down = event.key === "ArrowDown" || (event.key === "j" && !typing);
    const up = event.key === "ArrowUp" || (event.key === "k" && !typing);

    if (down) {
      event.preventDefault();
      this.focusResult(list, at + 1);
    } else if (up) {
      event.preventDefault();
      if (at <= 0) search?.focus();
      else this.focusResult(list, at - 1);
    } else if (event.key === "Enter" && inSearch) {
      const shown = document.querySelector("#main [data-query]")?.dataset.query ?? null;
      if (shown !== null && shown === search.value.trim()) {
        event.preventDefault();
        list[0].click();
      }
    }
  },
}));

HyperChi.start();
