// Runs before first paint (external file, so it satisfies the CSP) to avoid a flash of the wrong theme.
(function () {
  var t = null;
  try { t = localStorage.getItem("dapper-theme"); } catch (e) {}
  if (t !== "light" && t !== "dark" && t !== "sepia") {
    t = window.matchMedia && window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
  }
  document.documentElement.setAttribute("data-theme", t);
})();
