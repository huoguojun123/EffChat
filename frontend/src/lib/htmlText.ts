export function escapeHtml(value: string) {
  return value
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;")
}

export function compactCodeForDisplay(code: string) {
  return code
    .split("\n")
    .filter((line) => line.trim() !== "")
    .join("\n")
}
