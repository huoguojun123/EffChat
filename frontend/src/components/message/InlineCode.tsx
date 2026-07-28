import { memo, useEffect, useState } from "react"
import { escapeHtml } from "@/lib/htmlText"
import { colorTheme } from "@/lib/themes"
import { useUIStore } from "@/stores/ui"

interface Props {
  code: string
}

export const InlineCode = memo(function InlineCode({ code }: Props) {
  const lightColorTheme = useUIStore((s) => s.lightColorTheme)
  const darkColorTheme = useUIStore((s) => s.darkColorTheme)
  const [html, setHtml] = useState(() => escapeHtml(code))

  useEffect(() => {
    let canceled = false
    setHtml(escapeHtml(code))
    void import("@/lib/syntaxHighlight")
      .then(({ highlightInlineCodeToHtml }) => highlightInlineCodeToHtml(
        code,
        colorTheme(lightColorTheme).shikiLight,
        colorTheme(darkColorTheme).shikiDark
      ))
      .then((nextHtml) => {
        if (!canceled) setHtml(nextHtml)
      })
      .catch(() => {
        if (!canceled) setHtml(escapeHtml(code))
      })
    return () => {
      canceled = true
    }
  }, [code, darkColorTheme, lightColorTheme])

  // html comes from Shiki's inline code output or escapeHtml fallback, never raw markdown HTML.
  return <code className="inline-code-highlight shiki" dangerouslySetInnerHTML={{ __html: html }} />
})
