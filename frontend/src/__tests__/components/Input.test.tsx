import { describe, expect, it } from "vitest"
import { renderToStaticMarkup } from "react-dom/server"
import { Input } from "@/components/ui/input"

describe("Input", () => {
  it("keeps password-manager exclusion on non-credential fields", () => {
    const html = renderToStaticMarkup(<Input type="search" autoComplete="off" />)

    expect(html).toContain('autoComplete="off"')
    expect(html).toContain('data-1p-ignore="true"')
    expect(html).toContain('data-lpignore="true"')
  })

  it("preserves explicit credential autofill semantics", () => {
    const html = renderToStaticMarkup(<Input type="password" autoComplete="current-password" />)

    expect(html).toContain('autoComplete="current-password"')
    expect(html).not.toContain("data-1p-ignore")
    expect(html).not.toContain("data-lpignore")
  })
})
