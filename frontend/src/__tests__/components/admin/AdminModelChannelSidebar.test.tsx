import { describe, expect, it } from "vitest"
import { renderToStaticMarkup } from "react-dom/server"
import { AdminModelChannelSidebar } from "@/components/admin/AdminModelChannelSidebar"

describe("AdminModelChannelSidebar", () => {
  it("keeps the model filter out of browser autofill identities", () => {
    const html = renderToStaticMarkup(
      <AdminModelChannelSidebar
        visible
        grouped={[]}
        selectedProvider="openai"
        unconfiguredProviderKey="__unconfigured__"
        channelLabels={{}}
        query=""
        onQueryChange={() => {}}
        onSelectProvider={() => {}}
        onCreateChannel={() => {}}
      />,
    )

    expect(html).toContain('name="effchat-model-filter"')
    expect(html).toContain('autoComplete="off"')
    expect(html).toContain('data-1p-ignore="true"')
    expect(html).toContain('data-lpignore="true"')
    expect(html).not.toContain('name="effchat-model-search"')
  })
})
