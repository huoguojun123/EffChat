import { sanitizeFontFamily } from "./diagramSvg"

export function buildGraphvizSandboxSrcDoc(
  svg: string,
  isDark: boolean,
  colors?: { background: string; foreground: string; fontFamily?: string },
) {
  const background = colors?.background || (isDark ? "#111827" : "#ffffff")
  const foreground = colors?.foreground || (isDark ? "#e5e7eb" : "#171717")
  const fontFamily = sanitizeFontFamily(colors?.fontFamily || "")
  return `<!doctype html>
<html lang="zh-CN">
  <head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <style>
      html, body {
        margin: 0;
        width: 100%;
        height: 100%;
        background: ${background};
        color: ${foreground};
      }
      body {
        box-sizing: border-box;
        display: flex;
        align-items: center;
        justify-content: center;
        overflow: hidden;
      }
      svg {
        display: block;
        max-width: none;
        max-height: none;
      }
      text, tspan {
        font-family: ${fontFamily} !important;
      }
      a {
        pointer-events: none;
        cursor: default;
      }
    </style>
  </head>
  <body>
    ${svg}
  </body>
</html>`
}
