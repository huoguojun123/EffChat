// 上传前把大图压缩到目标大小内，省带宽、省上下文 token、避免后端体积上限拦截。
// 用 canvas 等比缩放 + 递减质量重编码为 jpeg/webp。svg/gif 跳过（gif 会丢动画，svg 是矢量）。

const TARGET_BYTES = 500 * 1024
const MAX_DIMENSION = 2048

const SKIP_TYPES = ["image/svg+xml", "image/gif"]

export async function compressImageIfNeeded(file: File): Promise<File> {
  if (!file.type.startsWith("image/")) return file
  if (SKIP_TYPES.includes(file.type)) return file
  if (file.size <= TARGET_BYTES) return file

  let bitmap: ImageBitmap
  try {
    bitmap = await createImageBitmap(file)
  } catch {
    return file // 解码失败就原样上传，交给后端校验
  }

  let { width, height } = bitmap
  if (width > MAX_DIMENSION || height > MAX_DIMENSION) {
    const scale = MAX_DIMENSION / Math.max(width, height)
    width = Math.round(width * scale)
    height = Math.round(height * scale)
  }

  const canvas = document.createElement("canvas")
  canvas.width = width
  canvas.height = height
  const ctx = canvas.getContext("2d")
  if (!ctx) {
    bitmap.close()
    return file
  }
  ctx.drawImage(bitmap, 0, 0, width, height)
  bitmap.close()

  // 优先 webp（压缩率更高），不支持则退回 jpeg；质量从 0.85 递减直到达标。
  const mime = supportsWebp(canvas) ? "image/webp" : "image/jpeg"
  for (const quality of [0.85, 0.7, 0.55, 0.4]) {
    const blob = await canvasToBlob(canvas, mime, quality)
    if (blob && blob.size <= TARGET_BYTES) {
      return blobToFile(blob, file.name, mime)
    }
    if (quality === 0.4 && blob) {
      // 最低质量仍超标：取它已是最小，直接返回（仍比原图小）。
      return blobToFile(blob, file.name, mime)
    }
  }
  return file
}

function supportsWebp(canvas: HTMLCanvasElement): boolean {
  return canvas.toDataURL("image/webp").startsWith("data:image/webp")
}

function canvasToBlob(canvas: HTMLCanvasElement, type: string, quality: number): Promise<Blob | null> {
  return new Promise((resolve) => canvas.toBlob(resolve, type, quality))
}

function blobToFile(blob: Blob, originalName: string, mime: string): File {
  const ext = mime === "image/webp" ? ".webp" : ".jpg"
  const base = originalName.replace(/\.[^.]+$/, "")
  return new File([blob], `${base}${ext}`, { type: mime })
}
