export interface PreparedDiagramSvg {
  svg: string
  width: number
  height: number
  maxTextSize: number
  initialFocus?: { x: number; y: number }
}

const defaultSize = { width: 800, height: 450 }
const fallbackDiagramFontFamily = 'Georgia, "Noto Serif SC", "Source Han Serif SC", "Songti SC", serif'

export function prepareDiagramSvg(svg: string): PreparedDiagramSvg {
  const viewBox = svg.match(/\bviewBox=["']\s*[-\d.]+\s+[-\d.]+\s+([\d.]+)\s+([\d.]+)\s*["']/i)
  const openingTag = svg.match(/<svg\b[^>]*>/i)?.[0]
  const width = svgLength(openingTag?.match(/\bwidth=["']([^"']+)["']/i)?.[1]) || positiveNumber(viewBox?.[1]) || defaultSize.width
  const height = svgLength(openingTag?.match(/\bheight=["']([^"']+)["']/i)?.[1]) || positiveNumber(viewBox?.[2]) || defaultSize.height
  const maxTextSize = representativeSvgFontSize(svg) * svgCoordinateScale(width, height, viewBox)
  const initialFocus = firstNodeFocus(svg)
  if (!openingTag) return { svg, width, height, maxTextSize, initialFocus }

  let normalizedTag = openingTag
    .replace(/\swidth=["'][^"']*["']/i, "")
    .replace(/\sheight=["'][^"']*["']/i, "")
    .replace(/\spreserveAspectRatio=["'][^"']*["']/i, "")

  if (/\sstyle=["'][^"']*["']/i.test(normalizedTag)) {
    normalizedTag = normalizedTag.replace(/\sstyle=(["'])(.*?)\1/i, (_match, quote: string, style: string) => {
      const cleaned = style.replace(/(?:^|;)\s*max-width\s*:[^;]*/gi, "").replace(/^;+|;+$/g, "")
      return ` style=${quote}${cleaned}${cleaned ? ";" : ""}max-width:none;overflow:visible${quote}`
    })
  } else {
    normalizedTag = normalizedTag.replace(/>$/, ' style="max-width:none;overflow:visible">')
  }

  normalizedTag = normalizedTag.replace(/>$/, ` width="${width}" height="${height}" preserveAspectRatio="xMidYMid meet">`)
  return { svg: svg.replace(openingTag, normalizedTag), width, height, maxTextSize, initialFocus }
}

export function normalizeMermaidTypography(svg: string, fontFamily = readDiagramFontFamily()) {
  if (typeof document === "undefined") return svg

  const host = document.createElement("div")
  host.setAttribute("aria-hidden", "true")
  host.style.cssText = "position:fixed;left:-100000px;top:0;visibility:hidden;pointer-events:none;contain:layout style paint;"
  host.innerHTML = svg
  document.body.append(host)

  try {
    const svgElement = host.querySelector("svg")
    if (!svgElement) return svg

    const entries = collectDiagramTextEntries(host)
    const bodyFontSize = representativeBodyFontSize(entries)
    const safeFontFamily = sanitizeFontFamily(fontFamily)

    for (const entry of entries) {
      const styles = [entry.element.getAttribute("style") || ""]
      if (safeFontFamily) styles.push(`font-family:${safeFontFamily}!important`)
      if (bodyFontSize > 0 && isDiagramTitle(entry.element)) styles.push(`font-size:${bodyFontSize}px!important`)
      entry.element.setAttribute("style", styles.filter(Boolean).join(";"))
    }
    return svgElement.outerHTML
  } finally {
    host.remove()
  }
}

export function readDiagramFontFamily() {
  if (typeof document === "undefined") return fallbackDiagramFontFamily

  const probe = document.createElement("span")
  if (!probe?.style) return fallbackDiagramFontFamily
  probe.style.cssText = "position:fixed;left:-100000px;font-family:var(--chat-font-family,var(--font-serif));"
  document.body.append(probe)
  try {
    return getComputedStyle(probe).fontFamily || fallbackDiagramFontFamily
  } finally {
    probe.remove()
  }
}

export function readResolvedThemeColor(property: string, fallback: string) {
  if (typeof document === "undefined") return fallback

  const probe = document.createElement("span")
  if (!probe?.style) return fallback
  probe.style.cssText = `position:fixed;left:-100000px;color:var(${property});`
  document.body.append(probe)
  try {
    return getComputedStyle(probe).color || fallback
  } finally {
    probe.remove()
  }
}

export function sanitizeFontFamily(fontFamily: string) {
  return fontFamily.replace(/[;{}<>]/g, "").trim() || fallbackDiagramFontFamily
}

export function measureRenderedDiagramTextSize(svg: string, fallback: number) {
  if (typeof document === "undefined") return fallback

  const host = document.createElement("div")
  host.setAttribute("aria-hidden", "true")
  host.style.cssText = "position:fixed;left:-100000px;top:0;visibility:hidden;pointer-events:none;contain:layout style paint;"
  host.innerHTML = svg
  document.body.append(host)

  try {
    const svgElement = host.querySelector("svg")
    if (!svgElement) return fallback

    const textSize = representativeTextSize(collectDiagramTextEntries(host), svgElement)
    return textSize || fallback
  } finally {
    host.remove()
  }
}

function representativeTextSize(entries: Array<{ element: Element; fontSize: number; text: string }>, svg: SVGSVGElement) {
  const visible = entries.filter((entry) => entry.text)
  if (!visible.length) return 0

  const coordinateScale = renderedSvgCoordinateScale(svg)
  return Math.max(...visible.map((entry) => entry.fontSize)) * coordinateScale
}

function collectDiagramTextEntries(root: ParentNode) {
  const entries: Array<{ element: Element; fontSize: number; text: string }> = []
  for (const element of root.querySelectorAll("text, foreignObject, foreignObject *")) {
    const fontSize = Number.parseFloat(getComputedStyle(element).fontSize)
    if (!Number.isFinite(fontSize)) continue
    entries.push({ element, fontSize, text: element.textContent?.trim() || "" })
  }
  return entries
}

function representativeBodyFontSize(entries: Array<{ element: Element; fontSize: number; text: string }>) {
  const body = entries.filter((entry) => entry.text && !isDiagramTitle(entry.element))
  const candidates = body.length ? body : entries.filter((entry) => entry.text)
  return candidates.length ? Math.max(...candidates.map((entry) => entry.fontSize)) : 0
}

function isDiagramTitle(element: Element) {
  const className = element.getAttribute("class") || ""
  if (/(?:^|\s)[\w-]*title[\w-]*(?:\s|$)/i.test(className)) return true
  const svg = element.closest("svg")
  if (!svg) return false
  const text = element.textContent?.trim()
  const directTextCount = svg.querySelectorAll(":scope > text").length
  return Boolean(text && element.parentElement?.tagName.toLowerCase() === "svg" && element.tagName.toLowerCase() === "text" && (directTextCount > 1 || !className))
}

export function readableDiagramScale(maxTextSize: number, targetTextSize: number) {
  if (maxTextSize <= 0 || targetTextSize <= 0) return 1
  return targetTextSize / maxTextSize
}

function firstNodeFocus(svg: string) {
  for (const tag of svg.match(/<g\b[^>]*>/gi) || []) {
    const className = tag.match(/\bclass=["']([^"']+)["']/i)?.[1] || ""
    if (!className.split(/\s+/).includes("node")) continue
    const translate = tag.match(/\btransform=["']translate\(\s*([\d.-]+)[,\s]+([\d.-]+)\s*\)["']/i)
    if (!translate) continue
    return { x: Number.parseFloat(translate[1]), y: Number.parseFloat(translate[2]) }
  }
  return undefined
}

function positiveNumber(value?: string) {
  const parsed = Number.parseFloat(value || "")
  return Number.isFinite(parsed) && parsed > 0 ? parsed : 0
}

function representativeSvgFontSize(svg: string) {
  const openingSvg = svg.match(/<svg\b[^>]*>/i)?.[0] || ""
  const styles = [...svg.matchAll(/<style[^>]*>([\s\S]*?)<\/style>/gi)].map((match) => match[1]).join("\n")
  const globalFontSize = cssFontSize(openingSvg) || cssFontSize(styles.match(/font-size\s*:\s*([^;}]+)/i)?.[1])
  const textSizes: number[] = []

  for (const match of svg.matchAll(/<text\b([^>]*)>([\s\S]*?)<\/text>/gi)) {
    const attributes = match[1]
    const size = cssFontSize(attributes) || globalFontSize
    if (size > 0) textSizes.push(size)
  }

  return textSizes.length ? Math.max(...textSizes) : globalFontSize
}

function cssFontSize(value?: string) {
  if (!value) return 0
  const match = value.match(/\bfont-size\s*(?:=|:)\s*["']?\s*([\d.]+\s*(?:px|pt|in|cm|mm)?)/i)
    || value.match(/^\s*([\d.]+\s*(?:px|pt|in|cm|mm)?)\s*$/i)
  return svgLength(match?.[1])
}

function svgCoordinateScale(width: number, height: number, viewBox: RegExpMatchArray | null) {
  const viewBoxWidth = positiveNumber(viewBox?.[1])
  const viewBoxHeight = positiveNumber(viewBox?.[2])
  if (viewBoxWidth <= 0 || viewBoxHeight <= 0) return 1

  const widthScale = width / viewBoxWidth
  const heightScale = height / viewBoxHeight
  return Number.isFinite(widthScale) && Number.isFinite(heightScale) && widthScale > 0 && heightScale > 0
    ? Math.min(widthScale, heightScale)
    : 1
}

function renderedSvgCoordinateScale(svg: SVGSVGElement) {
  const { width, height } = svg.getBoundingClientRect()
  const viewBoxWidth = svg.viewBox.baseVal.width
  const viewBoxHeight = svg.viewBox.baseVal.height
  if (width <= 0 || height <= 0 || viewBoxWidth <= 0 || viewBoxHeight <= 0) return 1
  return Math.min(width / viewBoxWidth, height / viewBoxHeight)
}

function svgLength(value?: string) {
  if (!value) return 0
  const match = value.trim().match(/^([\d.]+)\s*(px|pt|in|cm|mm)?$/i)
  if (!match) return 0
  const valueNumber = positiveNumber(match[1])
  switch ((match[2] || "px").toLowerCase()) {
    case "pt": return valueNumber * (96 / 72)
    case "in": return valueNumber * 96
    case "cm": return valueNumber * (96 / 2.54)
    case "mm": return valueNumber * (96 / 25.4)
    default: return valueNumber
  }
}
