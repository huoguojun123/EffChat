const XLSX_MIME = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
const PPTX_MIME = "application/vnd.openxmlformats-officedocument.presentationml.presentation"
const DOCX_MIME = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"

const EXTENSION_TYPE_CANDIDATES: Record<string, string[]> = {
  ".md": ["text/markdown", "text/plain", "text/*"],
  ".markdown": ["text/markdown", "text/plain", "text/*"],
  ".csv": ["text/csv", "text/plain", "text/*", "application/vnd.ms-excel"],
  ".tsv": ["text/tab-separated-values", "text/plain", "text/*"],
  ".json": ["application/json", "text/plain", "text/*"],
  ".txt": ["text/plain", "text/*"],
  ".log": ["text/plain", "text/*"],
  ".yaml": ["application/x-yaml", "text/plain", "text/*"],
  ".yml": ["application/x-yaml", "text/plain", "text/*"],
  ".xml": ["application/xml", "text/xml", "text/*"],
  ".pdf": ["application/pdf"],
  ".docx": [DOCX_MIME],
  ".xlsx": [XLSX_MIME],
  ".pptx": [PPTX_MIME],
  ".png": ["image/png"],
  ".jpg": ["image/jpeg"],
  ".jpeg": ["image/jpeg"],
  ".gif": ["image/gif"],
  ".webp": ["image/webp"],
}

function matchesUploadType(type: string, allowedTypes: string[]): boolean {
  return allowedTypes.some((item) => {
    if (item === type) return true
    if (item.endsWith("/*")) return type.startsWith(item.slice(0, -1))
    return false
  })
}

export function isAcceptedFile(f: File, allowedTypes: string[]): boolean {
  const type = f.type || ""
  if (type && matchesUploadType(type, allowedTypes)) return true
  const lower = f.name.toLowerCase()
  const entry = Object.entries(EXTENSION_TYPE_CANDIDATES).find(([ext]) => lower.endsWith(ext))
  return Boolean(entry && entry[1].some((candidate) => matchesUploadType(candidate, allowedTypes)))
}

export function getClipboardFiles(data: Pick<DataTransfer, "files" | "items">): File[] {
  const directFiles = Array.from(data.files || []).filter((file) => file.size > 0)
  if (directFiles.length > 0) return directFiles

  return Array.from(data.items || [])
    .filter((item) => item.kind === "file")
    .map((item) => item.getAsFile())
    .filter((file): file is File => Boolean(file && file.size > 0))
}
