const fallbackFontFamily = 'Georgia, "Noto Serif SC", "Source Han Serif SC", "Songti SC", serif'

export function mermaidRenderConfig(
  isDark: boolean,
  colors?: { background: string; foreground: string; accent: string; fontFamily?: string },
) {
  const fontFamily = colors?.fontFamily || fallbackFontFamily
  return {
    startOnLoad: false,
    securityLevel: "strict" as const,
    theme: isDark ? "dark" as const : "default" as const,
    fontFamily,
    fontSize: 14,
    flowchart: {
      useMaxWidth: false,
    },
    sequence: {
      useMaxWidth: false,
    },
    gantt: {
      useMaxWidth: false,
      fontSize: 14,
      sectionFontSize: 14,
      barHeight: 24,
      barGap: 8,
      topPadding: 70,
    },
    journey: {
      useMaxWidth: false,
      taskFontSize: 14,
      titleFontSize: "14px",
    },
    timeline: {
      useMaxWidth: false,
      taskFontSize: 14,
    },
    themeCSS: `.titleText { font-size: 14px; } .grid .tick text { font-size: 14px; } text, foreignObject, foreignObject * { font-family: ${fontFamily} !important; }`,
    themeVariables: {
      fontFamily,
      pieTitleTextSize: "14px",
      pieSectionTextSize: "14px",
      pieLegendTextSize: "14px",
      ...(colors ? {
        background: colors.background,
        mainBkg: colors.background,
        primaryColor: colors.background,
        primaryTextColor: colors.foreground,
        primaryBorderColor: colors.accent,
        lineColor: colors.foreground,
        textColor: colors.foreground,
      } : {}),
    },
  }
}
