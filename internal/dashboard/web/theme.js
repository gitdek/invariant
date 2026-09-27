// Runs before the page paints, so it never flashes the wrong theme. A
// choice made with the switch wins; otherwise the system's setting does.
(() => {
  let theme = null;
  try { theme = localStorage.getItem("invariant-theme"); } catch (e) {}
  if (theme !== "light" && theme !== "dark") theme = matchMedia("(prefers-color-scheme: light)").matches ? "light" : "dark";
  document.documentElement.dataset.theme = theme;
})();
