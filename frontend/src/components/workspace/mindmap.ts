export interface MindMapNode {
  id: string
  label: string
  children: MindMapNode[]
}

export interface PositionedMindMapNode extends MindMapNode {
  depth: number
  x: number
  y: number
  width: number
  height: number
  lines: string[]
}

export interface MindMapLayout {
  nodes: PositionedMindMapNode[]
  connections: Array<{ from: PositionedMindMapNode; to: PositionedMindMapNode }>
  width: number
  height: number
}

const horizontalGap = 72
const verticalGap = 18
const outerPadding = 24
const maxTextWidth = 180

export function parseMindMap(source: string): MindMapNode[] {
  const roots: MindMapNode[] = []
  const stack: Array<{ indent: number; node: MindMapNode }> = []
  let sequence = 0

  for (const rawLine of source.split(/\r?\n/)) {
    if (!rawLine.trim()) continue
    const match = rawLine.match(/^([ \t]*)[-*+]\s+(.+?)\s*$/)
    if (!match) throw new Error("思维导图只接受无序列表，每一行需要以 -、* 或 + 开头")

    const indent = match[1].replace(/\t/g, "  ").length
    const node: MindMapNode = {
      id: `mindmap-${sequence++}`,
      label: normalizeLabel(match[2]),
      children: [],
    }

    while (stack.length && stack[stack.length - 1].indent >= indent) stack.pop()
    const parent = stack[stack.length - 1]?.node
    if (parent) parent.children.push(node)
    else roots.push(node)
    stack.push({ indent, node })
  }

  if (!roots.length) throw new Error("思维导图列表为空")
  return roots
}

export function layoutMindMap(roots: MindMapNode[]): MindMapLayout {
  const columnWidths: number[] = []
  const measurements = new Map<string, ReturnType<typeof measureNode>>()
  const subtreeHeights = new Map<string, number>()

  walk(roots, 0, (node, depth) => {
    const measurement = measureNode(node.label)
    measurements.set(node.id, measurement)
    columnWidths[depth] = Math.max(columnWidths[depth] || 0, measurement.width)
  })

  const columnX: number[] = []
  for (let depth = 0; depth < columnWidths.length; depth++) {
    columnX[depth] = depth === 0
      ? outerPadding
      : columnX[depth - 1] + columnWidths[depth - 1] + horizontalGap
  }

  const nodes: PositionedMindMapNode[] = []
  const connections: MindMapLayout["connections"] = []

  function measureSubtree(node: MindMapNode): number {
    const ownHeight = measurements.get(node.id)!.height
    if (!node.children.length) {
      subtreeHeights.set(node.id, ownHeight)
      return ownHeight
    }
    const childrenHeight = node.children.reduce((total, child) => total + measureSubtree(child), 0) + verticalGap * (node.children.length - 1)
    const height = Math.max(ownHeight, childrenHeight)
    subtreeHeights.set(node.id, height)
    return height
  }

  for (const root of roots) measureSubtree(root)

  function place(node: MindMapNode, depth: number, subtreeTop: number): PositionedMindMapNode {
    const measurement = measurements.get(node.id)!
    const subtreeHeight = subtreeHeights.get(node.id)!
    const y = subtreeTop + (subtreeHeight - measurement.height) / 2

    const positioned: PositionedMindMapNode = {
      ...node,
      depth,
      x: columnX[depth],
      y,
      ...measurement,
    }
    nodes.push(positioned)
    if (node.children.length) {
      const childrenHeight = node.children.reduce((total, child) => total + subtreeHeights.get(child.id)!, 0) + verticalGap * (node.children.length - 1)
      let childTop = subtreeTop + (subtreeHeight - childrenHeight) / 2
      for (const child of node.children) {
        const placedChild = place(child, depth + 1, childTop)
        connections.push({ from: positioned, to: placedChild })
        childTop += subtreeHeights.get(child.id)! + verticalGap
      }
    }
    return positioned
  }

  let rootTop = outerPadding
  for (const root of roots) {
    place(root, 0, rootTop)
    rootTop += subtreeHeights.get(root.id)! + verticalGap
  }
  const maxRight = Math.max(...nodes.map((node) => node.x + node.width))
  const maxBottom = Math.max(...nodes.map((node) => node.y + node.height))
  return {
    nodes,
    connections,
    width: Math.ceil(maxRight + outerPadding),
    height: Math.ceil(maxBottom + outerPadding),
  }
}

function walk(nodes: MindMapNode[], depth: number, visit: (node: MindMapNode, depth: number) => void) {
  for (const node of nodes) {
    visit(node, depth)
    walk(node.children, depth + 1, visit)
  }
}

function measureNode(label: string) {
  const lines = wrapLabel(label)
  const contentWidth = Math.max(...lines.map(textWidth))
  return {
    lines,
    width: Math.max(88, Math.min(maxTextWidth + 24, contentWidth + 24)),
    height: lines.length * 19 + 14,
  }
}

function wrapLabel(label: string) {
  const chars = Array.from(label)
  const lines: string[] = []
  let line = ""
  let width = 0
  for (const char of chars) {
    const charWidth = textWidth(char)
    if (line && width + charWidth > maxTextWidth) {
      lines.push(line)
      line = char
      width = charWidth
    } else {
      line += char
      width += charWidth
    }
  }
  if (line) lines.push(line)
  return lines.length ? lines : [""]
}

function textWidth(value: string) {
  return Array.from(value).reduce((width, char) => width + ((char.codePointAt(0) || 0) > 0xff ? 14 : 7.5), 0)
}

function normalizeLabel(value: string) {
  return value.trim()
    .replace(/^\*\*(.+)\*\*$/, "$1")
    .replace(/^__(.+)__$/, "$1")
    .replace(/^`(.+)`$/, "$1")
}
