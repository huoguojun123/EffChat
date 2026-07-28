export type GraphvizSvgMetrics = {
  width: number
  height: number
}

export function parseGraphvizSvgMetrics(svg: string): GraphvizSvgMetrics | null {
  if (!svg.trim()) return null

  // Graphviz writes its rendered CSS size in pt while viewBox remains in SVG
  // user units. The iframe renders the physical size, so prefer it here; using
  // viewBox first makes a 72pt diagram occupy only 72 CSS pixels instead of 96.
  const width = parseSvgLength(svg.match(/\bwidth=["']([^"']+)["']/i)?.[1])
  const height = parseSvgLength(svg.match(/\bheight=["']([^"']+)["']/i)?.[1])
  if (width > 0 && height > 0) return { width, height }

  const viewBox = svg.match(/\bviewBox=["']\s*([-\d.]+)\s+([-\d.]+)\s+([-\d.]+)\s+([-\d.]+)\s*["']/i)
  if (viewBox) {
    const width = Number.parseFloat(viewBox[3])
    const height = Number.parseFloat(viewBox[4])
    if (width > 0 && height > 0) return { width, height }
  }
  return null
}

function parseSvgLength(value?: string) {
  if (!value) return 0
  const match = value.trim().match(/^([0-9.]+)\s*(px|pt|in|cm|mm)?$/i)
  if (!match) return 0
  const n = Number.parseFloat(match[1])
  if (!Number.isFinite(n) || n <= 0) return 0
  switch ((match[2] || "px").toLowerCase()) {
    case "pt":
      return n * (96 / 72)
    case "in":
      return n * 96
    case "cm":
      return n * (96 / 2.54)
    case "mm":
      return n * (96 / 25.4)
    default:
      return n
  }
}
