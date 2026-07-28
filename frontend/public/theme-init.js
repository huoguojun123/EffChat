;(function () {
  var mode = localStorage.getItem("theme") || "system"
  var dark = mode === "dark" || (mode === "system" && matchMedia("(prefers-color-scheme:dark)").matches)
  var root = document.documentElement
  var lightTheme = localStorage.getItem("light_color_theme") || "codex"
  var darkTheme = localStorage.getItem("dark_color_theme") || "codex"
  var accent = localStorage.getItem("accent_color") || "default"
  var theme = dark ? darkTheme : lightTheme
  var colors = {
    codex: ["#faf9f5", "#171615"],
    github: ["#f0f6fc", "#080c12"],
    parchment: ["#f4ebdc", "#211914"],
    catppuccin: ["#e7e9f5", "#11111b"],
    everforest: ["#f1ebcf", "#222b28"],
    gruvbox: ["#f9e4ad", "#1d2021"],
    one: ["#f3f5fa", "#1e222a"],
  }

  root.classList.toggle("dark", dark)
  root.dataset.colorTheme = theme
  root.dataset.accent = accent
  root.style.colorScheme = dark ? "dark" : "light"
  document.querySelector('meta[name="theme-color"]')?.setAttribute("content", (colors[theme] || colors.codex)[dark ? 1 : 0])
})()
