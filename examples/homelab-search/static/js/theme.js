// Apply a saved light theme before the first paint. Dark is the default, and
// it stays dark when storage is unavailable.
try {
  if (localStorage.getItem("homelab-search-theme") === "light")
    document.documentElement.dataset.theme = "light";
} catch {
  /* Private windows can block storage. */
}
