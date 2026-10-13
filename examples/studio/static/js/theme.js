// Runs in <head> before the first paint: apply the remembered theme so a dark
// choice never flashes light. Without a stored choice the system preference
// applies through CSS. Storage can be unavailable (private mode, policy).
try {
  const theme = localStorage.getItem("driftlamp-theme");
  if (theme === "dark" || theme === "light") {
    document.documentElement.dataset.theme = theme;
  }
} catch {
  // The system preference stays in charge.
}
