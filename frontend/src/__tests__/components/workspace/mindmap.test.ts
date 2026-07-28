import { describe, expect, it } from "vitest"
import { layoutMindMap, parseMindMap } from "@/components/workspace/mindmap"

describe("plain list mind maps", () => {
  it("parses nested unordered lists without Mermaid syntax", () => {
    const roots = parseMindMap(`- 408 复习
  - 数据结构
    - 树与二叉树
  - 操作系统`)

    expect(roots).toHaveLength(1)
    expect(roots[0].label).toBe("408 复习")
    expect(roots[0].children.map((node) => node.label)).toEqual(["数据结构", "操作系统"])
    expect(roots[0].children[0].children[0].label).toBe("树与二叉树")
  })

  it("rejects prose mixed into a mind map block", () => {
    expect(() => parseMindMap("主题\n- 分支")).toThrow("只接受无序列表")
  })

  it("lays out children to the right with readable natural dimensions", () => {
    const layout = layoutMindMap(parseMindMap("- 根节点\n  - 子节点 A\n  - 子节点 B"))
    const root = layout.nodes.find((node) => node.depth === 0)!
    const children = layout.nodes.filter((node) => node.depth === 1)

    expect(children.every((node) => node.x > root.x + root.width)).toBe(true)
    expect(children[1].y).toBeGreaterThan(children[0].y)
    expect(layout.width).toBeGreaterThan(250)
  })

  it("keeps tall parents and following roots from overlapping", () => {
    const longTitle = "这是一个很长的根节点标题，需要换成多行并完整保留原始语义".repeat(3)
    const layout = layoutMindMap(parseMindMap(`- ${longTitle}\n  - 唯一子节点\n- 第二个根节点`))
    const roots = layout.nodes.filter((node) => node.depth === 0).sort((a, b) => a.y - b.y)

    expect(roots).toHaveLength(2)
    expect(roots[1].y).toBeGreaterThanOrEqual(roots[0].y + roots[0].height + 18)
  })
})
