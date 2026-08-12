import { useEffect, useState } from "react"
import { fetchFileBlob } from "@/api/files"

interface BlobState {
  url: string | null
  loading: boolean
  error: boolean
}

interface BlobOwner {
  fileId: number | undefined
  generation: number
}

interface OwnedBlobState extends BlobState {
  owner: BlobOwner
}

const IDLE: BlobState = { url: null, loading: false, error: false }
const LOADING: BlobState = { url: null, loading: true, error: false }

// useAuthedBlobUrl 拉取鉴权文件并转成 objectURL 供 <img> 使用。
// 鉴权走 Authorization header（token 在 localStorage），<img src> 无法直接带，
// 故先 fetch blob 再 createObjectURL；卸载时 revoke 释放，避免内存泄漏。
//
// 状态与当前 render owner 分开保存：文件切换的首个 committed render 必须立即显示
// loading，而不能等待 effect 才清掉上一文件的 URL 或错误。
export function useAuthedBlobUrl(fileId: number | undefined): BlobState {
  const [renderOwner, setRenderOwner] = useState<BlobOwner>(() => ({ fileId, generation: 0 }))
  let owner = renderOwner
  if (renderOwner.fileId !== fileId) {
    // React immediately retries this component before committing descendants.
    // Advancing a monotonic generation also distinguishes A→B→A, so the
    // first A's already-revoked URL cannot become visible during the return.
    owner = { fileId, generation: renderOwner.generation + 1 }
    setRenderOwner(owner)
  }
  const [state, setState] = useState<OwnedBlobState | null>(null)

  useEffect(() => {
    if (fileId === undefined) return
    const controller = new AbortController()
    let cancelled = false
    let objectUrl: string | null = null
    fetchFileBlob(fileId, controller.signal)
      .then((blob) => {
        if (cancelled) return
        objectUrl = URL.createObjectURL(blob)
        setState({ owner, url: objectUrl, loading: false, error: false })
      })
      .catch(() => {
        if (!cancelled) setState({ owner, url: null, loading: false, error: true })
      })
    return () => {
      cancelled = true
      controller.abort()
      if (objectUrl) URL.revokeObjectURL(objectUrl)
    }
  }, [fileId, owner])

  return visibleBlobState(fileId, owner, state)
}

function sameBlobOwner(left: BlobOwner, right: BlobOwner) {
  return left.fileId === right.fileId && left.generation === right.generation
}

export function visibleBlobState(fileId: number | undefined, owner: BlobOwner, state: OwnedBlobState | null): BlobState {
  if (fileId === undefined) return IDLE
  return state && sameBlobOwner(state.owner, owner) ? state : LOADING
}
