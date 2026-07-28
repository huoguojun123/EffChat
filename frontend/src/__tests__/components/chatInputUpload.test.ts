import { describe, expect, it } from "vitest"
import { getClipboardFiles, isAcceptedFile } from "@/components/chat/chatInputUpload"

describe("chat input upload helpers", () => {
  it("accepts pasted image files from clipboard file list", () => {
    const image = new File(["png"], "pasted.png", { type: "image/png" })
    const files = getClipboardFiles({
      files: [image] as unknown as FileList,
      items: [] as unknown as DataTransferItemList,
    })

    expect(files).toEqual([image])
    expect(isAcceptedFile(image, ["image/png"])).toBe(true)
  })

  it("falls back to clipboard items when files is empty", () => {
    const image = new File(["jpg"], "clipboard.jpg", { type: "image/jpeg" })
    const files = getClipboardFiles({
      files: [] as unknown as FileList,
      items: [
        { kind: "string", getAsFile: () => null },
        { kind: "file", getAsFile: () => image },
      ] as unknown as DataTransferItemList,
    })

    expect(files).toEqual([image])
  })
})
