import { animate, stagger } from "./animejs.esm.js";

const reducedMotion = matchMedia("(prefers-reduced-motion: reduce)");
const smallScreen = matchMedia("(max-width: 767px)");
let sculptureLoading;
function loadSculpture() {
  sculptureLoading ||= import("./sculpture.js").catch(() => {
    const control = document.querySelector(".motion-control");
    if (control) control.hidden = true;
  });
  return sculptureLoading;
}
const systemTheme = matchMedia("(prefers-color-scheme: dark)");
const animations = new Set();
function play(targets, options) {
  if (reducedMotion.matches) return;
  const animation = animate(targets, options);
  animations.add(animation);
  animation.then(() => animations.delete(animation));
}

// Shared clipboard path for the install command, code blocks, and pages.
// When the Clipboard API is unavailable it selects fallbackNode instead.
async function copyText(text, fallbackNode) {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    const selection = window.getSelection();
    if (fallbackNode && selection) {
      const range = document.createRange();
      range.selectNodeContents(fallbackNode);
      selection.removeAllRanges();
      selection.addRange(range);
    }
    return false;
  }
}
const labelTimers = new WeakMap();
function flashLabel(button, text) {
  if (!button) return;
  if (!button.dataset.label) button.dataset.label = button.textContent;
  button.textContent = text;
  clearTimeout(labelTimers.get(button));
  labelTimers.set(
    button,
    setTimeout(() => {
      button.textContent = button.dataset.label;
    }, 2000),
  );
}

HyperChi.registerComponent("site", () => ({
  menu: false,
  paused: reducedMotion.matches || smallScreen.matches,
  copyLabel: "Copy",
  copyStatus: "",
  query: "",
  count: 0,
  init() {
    this.onMotionChange = (event) => {
      this.paused = event.matches || smallScreen.matches;
      if (!this.paused && document.getElementById("sculpture")) loadSculpture();
      document.dispatchEvent(
        new CustomEvent("hyperchi:motion", { detail: { paused: this.paused } }),
      );
    };
    reducedMotion.addEventListener("change", this.onMotionChange);
  },
  toggleTheme() {
    const current =
      document.documentElement.dataset.theme ||
      (systemTheme.matches ? "dark" : "light");
    const theme = current === "dark" ? "light" : "dark";
    document.documentElement.dataset.theme = theme;
    try {
      localStorage.setItem("hyperchi-theme", theme);
    } catch {
      /* In-memory selection still works. */
    }
  },
  toggleMotion() {
    this.paused = !this.paused;
    const host = document.getElementById("sculpture");
    if (host) host.dataset.paused = String(this.paused);
    if (!this.paused && !reducedMotion.matches) loadSculpture();
    document.dispatchEvent(
      new CustomEvent("hyperchi:motion", { detail: { paused: this.paused } }),
    );
  },
  async copyInstall() {
    const code = document.querySelector(".install-command code");
    if (await copyText("go get github.com/regiellis/hyperchi", code)) {
      this.copyLabel = "Copied";
      this.announce("Install command copied.");
    } else {
      this.copyLabel = "Select command";
      this.announce(
        "Clipboard unavailable. Select and copy the command beside this button.",
      );
    }
  },
  // Every docs code block has a server-rendered copy button. The handler is an
  // hc-on directive, so buttons in swapped-in HTML work without rebinding.
  async copyCode(event) {
    const button = event.currentTarget;
    const code = button?.closest(".code-block")?.querySelector("code");
    if (!code) return;
    const copied = await copyText(code.textContent, code);
    flashLabel(button, copied ? "Copied" : "Selected");
    this.announce(
      copied
        ? "Copied"
        : "Clipboard unavailable. The code is selected; copy it with your keyboard.",
    );
  },
  // Copies the page's Markdown source. The fetch runs inside the clipboard
  // write where the browser allows it, so Safari keeps the user gesture.
  async copyPage(event) {
    const button = event.currentTarget;
    const url = button?.dataset.markdown;
    if (!url) return;
    const markdown = fetch(url, { headers: { Accept: "text/markdown" } }).then(
      (response) => {
        if (!response.ok) throw new Error(`HTTP ${response.status}`);
        return response.text();
      },
    );
    markdown.catch(() => {});
    let copied = false;
    try {
      if (typeof ClipboardItem === "function" && navigator.clipboard?.write) {
        const blob = markdown.then((text) => new Blob([text], { type: "text/plain" }));
        await navigator.clipboard.write([new ClipboardItem({ "text/plain": blob })]);
        copied = true;
      }
    } catch {
      copied = false;
    }
    if (!copied) {
      try {
        copied = await copyText(await markdown);
      } catch {
        copied = false;
      }
    }
    flashLabel(button, copied ? "Copied" : "Copy failed");
    this.announce(
      copied
        ? "Page copied as Markdown."
        : "Copy failed. Open View as Markdown and copy the text instead.",
    );
  },
  // Repeating the same message must still be announced, so the live region
  // empties before it speaks again.
  announce(message) {
    this.copyStatus = "";
    setTimeout(() => {
      this.copyStatus = message;
    }, 60);
  },
  // "/" and Ctrl/Cmd+K focus the docs search from anywhere on the page.
  shortcut(event) {
    const input = document.getElementById("docs-q");
    if (!input || event.defaultPrevented) return;
    const key = event.key || "";
    const command = (event.ctrlKey || event.metaKey) && key.toLowerCase() === "k";
    const typing = event.target.closest?.(
      "input, textarea, select, [contenteditable]",
    );
    const slash = key === "/" && !typing && !event.ctrlKey && !event.metaKey && !event.altKey;
    if (!command && !slash) return;
    event.preventDefault();
    input.focus();
    input.select();
  },
  searchKeydown(event) {
    if (event.key !== "Escape") return;
    const results = document.getElementById("docs-search-results");
    if (results) results.replaceChildren();
    event.currentTarget.value = "";
  },
  matches(label) {
    return label.toLowerCase().includes(this.query.trim().toLowerCase());
  },
  get noResults() {
    return ![...document.querySelectorAll(".docs-sidebar nav a")].some((link) =>
      this.matches(link.textContent),
    );
  },
}));
HyperChi.start();

// Text is visible in the server HTML. Animation enhances it after boot.
play("[data-enter]", {
  opacity: [0, 1],
  y: [24, 0],
  delay: stagger(100),
  duration: 800,
  ease: "outCubic",
});
const observer = new IntersectionObserver(
  (entries) => {
    for (const entry of entries) {
      if (!entry.isIntersecting) continue;
      play(entry.target, {
        opacity: [0.35, 1],
        y: [25, 0],
        duration: 700,
        ease: "outCubic",
      });
      observer.unobserve(entry.target);
    }
  },
  { threshold: 0.15 },
);
document
  .querySelectorAll("[data-reveal]")
  .forEach((element) => observer.observe(element));

reducedMotion.addEventListener("change", (event) => {
  if (event.matches) {
    for (const animation of animations) animation.complete();
    animations.clear();
  }
});

// Keep the public activity stream bounded during long visits.
const activityLog = document.getElementById("activity-log");
const activityObserver = activityLog
  ? new MutationObserver(() => {
      while (activityLog.childElementCount > 50) activityLog.firstElementChild.remove();
    })
  : null;
if (activityObserver) activityObserver.observe(activityLog, { childList: true });

// Open the metrics stream only when its readout is on screen.
const streams = new IntersectionObserver(
  (entries) => {
    for (const entry of entries) {
      if (!entry.isIntersecting) continue;
      entry.target.setAttribute("hx-sse:connect", entry.target.dataset.stream);
      htmx.process(entry.target);
      streams.unobserve(entry.target);
    }
  },
  { rootMargin: "120px" },
);
document
  .querySelectorAll("[data-stream]")
  .forEach((element) => streams.observe(element));

function showHashTarget() {
  let target;
  try {
    target = document.getElementById(
      decodeURIComponent(location.hash.slice(1)),
    );
  } catch {
    return;
  }
  if (target instanceof HTMLDetailsElement) {
    target.open = true;
    target.scrollIntoView({ behavior: "instant", block: "start" });
  }
}
showHashTarget();
window.addEventListener("hashchange", showHashTarget);

document.body.addEventListener("htmx:before:request", (event) => {
  if (event.detail?.ctx) {
    const error = document.querySelector("#request-error");
    if (error) error.hidden = true;
  }
});
document.body.addEventListener("htmx:after:swap", (event) => {
  const target = event.detail?.ctx?.target;
  if (target === "#echo-out" || target?.id === "echo-out") {
    play(".request-symbol", {
      rotate: [0, 360],
      scale: [1, 1.18, 1],
      duration: 800,
      ease: "outCubic",
    });
    document.dispatchEvent(new Event("hyperchi:request-complete"));
  }
});
for (const eventName of ["htmx:response:error", "htmx:error"]) {
  document.body.addEventListener(eventName, (event) => {
    // Validation errors render beside their forms; only transport/server errors use this notice.
    const status = event.detail?.ctx?.response?.status;
    if (status && status < 500) return;
    const notice = document.querySelector("#request-error");
    if (notice) {
      notice.textContent =
        "The request could not complete. Check your connection and try again.";
      notice.hidden = false;
    }
  });
}
document.body.addEventListener("autoSaved", () => {
  const status = document.getElementById("draft-status");
  if (status) status.textContent = "Draft saved";
});

// Mobile starts with the same artwork and offers animation on demand.
// Docs and reduced-motion visits never download Three.js.
if (
  document.getElementById("sculpture") &&
  !smallScreen.matches &&
  !reducedMotion.matches
) {
  if ("requestIdleCallback" in window)
    requestIdleCallback(loadSculpture, { timeout: 2000 });
  else loadSculpture();
}
window.addEventListener("pagehide", (event) => {
  if (!event.persisted) {
    observer.disconnect();
    streams.disconnect();
    activityObserver?.disconnect();
  }
  for (const animation of animations) animation.complete();
  animations.clear();
});
