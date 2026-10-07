// The site's one hc-* scope, on <body hc-data="site()">. It owns the theme
// toggle; everything else on the site is server-rendered HTML and htmx.
const systemDark = matchMedia("(prefers-color-scheme: dark)");
const reducedMotion = matchMedia("(prefers-reduced-motion: reduce)");

function currentTheme() {
  return document.documentElement.dataset.theme || (systemDark.matches ? "dark" : "light");
}

HyperChi.registerComponent("site", () => ({
  dark: currentTheme() === "dark",
  init() {
    // Follow the system while the visitor has not chosen a theme.
    systemDark.addEventListener("change", () => {
      this.dark = currentTheme() === "dark";
    });
  },
  toggleTheme() {
    const theme = this.dark ? "light" : "dark";
    document.documentElement.dataset.theme = theme;
    this.dark = theme === "dark";
    try {
      localStorage.setItem("driftlamp-theme", theme);
    } catch {
      // The choice still applies to this page.
    }
  },
}));

// After htmx swaps the devlog into #main, move focus and the viewport to the
// new heading and update the title, as a full page load would.
document.addEventListener("htmx:after:swap", (event) => {
  const main = document.getElementById("main");
  const target = event.detail?.ctx?.target;
  if (!main || (target !== main && target !== "#main")) return;
  const page = main.querySelector("[data-page-title]");
  if (page) document.title = page.dataset.pageTitle;
  const heading = main.querySelector("h1");
  if (heading) {
    heading.focus({ preventScroll: true });
    heading.scrollIntoView({ behavior: reducedMotion.matches ? "instant" : "smooth", block: "start" });
  }
});

HyperChi.start();
