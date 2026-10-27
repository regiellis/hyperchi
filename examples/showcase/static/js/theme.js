// Apply a remembered preference before CSS paints. Storage can be unavailable.
try {
  const theme = localStorage.getItem("hyperchi-theme");
  if (theme === "dark" || theme === "light")
    document.documentElement.dataset.theme = theme;
} catch {
  /* System preference remains the fallback. */
}
