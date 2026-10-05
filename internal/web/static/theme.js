// Light/dark mode: "auto" follows the browser preference, "light" and
// "dark" override it. The choice is kept in localStorage. Loaded
// synchronously in <head> so the stored theme applies before first paint.
(() => {
  const modes = ["auto", "light", "dark"];
  const labels = { auto: "◐ auto", light: "☀ light", dark: "☾ dark" };
  const root = document.documentElement;

  const stored = () => {
    const m = localStorage.getItem("theme");
    return modes.includes(m) ? m : "auto";
  };
  const apply = (mode) => {
    if (mode === "auto") delete root.dataset.theme;
    else root.dataset.theme = mode;
  };
  apply(stored());

  document.addEventListener("DOMContentLoaded", () => {
    const button = document.getElementById("theme-switch");
    if (!button) return;
    const show = (mode) => {
      button.textContent = labels[mode];
      button.title = `Color scheme: ${mode} (click to change)`;
    };
    show(stored());
    button.hidden = false;
    button.addEventListener("click", () => {
      const next = modes[(modes.indexOf(stored()) + 1) % modes.length];
      if (next === "auto") localStorage.removeItem("theme");
      else localStorage.setItem("theme", next);
      apply(next);
      show(next);
    });
  });
})();
