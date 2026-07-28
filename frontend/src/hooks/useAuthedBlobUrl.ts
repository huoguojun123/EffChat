import { useEffect, useState } from "react"
import { fetchFileBlob } from "@/api/files"

interface BlobState {
  url: string | null
  loading: boolean
  error: boolean
}

const IDLE: BlobState = { url: null, loading: false, error: false }
const LOADING: BlobState = { url: null, loading: true, error: false }

// useAuthedBlobUrl 拉取鉴权文件并转成 objectURL 供 <img> 使用。
// 鉴权走 Authorization header（token 在 localStorage），<img src> 无法直接带，
// 故先 fetch blob 再 createObjectURL；卸载时 revoke 释放，避免内存泄漏。
//
// 状态合并为单个对象，且只在异步回调里 setState（effect 体内不同步 setState），
// 以满足 react-hooks 规则；初始 loading 由 useState 惰性初值给出，避免渲染期闪烁。
export function useAuthedBlobUrl(fileId: number | undefined): BlobState {
  const [state, setState] = useState<BlobState>(() => (fileId ? LOADING : IDLE))

  useEffect(() => {
    if (!fileId) {
      return
    }
    let revoked = false
    let objectUrl: string | null = null
    fetchFileBlob(fileId)
      .then((blob) => {
        if (revoked) return
        objectUrl = URL.createObjectURL(blob)
        setState({ url: objectUrl, loading: false, error: false })
      })
      .catch(() => {
        if (!revoked) setState({ url: null, loading: false, error: true })
      })
    return () => {
      revoked = true
      if (objectUrl) URL.revokeObjectURL(objectUrl)
      // 下次进入（fileId 变更）前回到 loading；fileId 被清空时无新 effect 重置，
      // 由下方 `fileId ? state : IDLE` 的渲染期派生兜底为 idle，避免卡在 loading。
      setState(LOADING)
    }
  }, [fileId])

  // fileId 为空时直接返回 IDLE，不依赖 cleanup 残留的 state（可能是上一个 fileId 的 LOADING）。
  return fileId ? state : IDLE
}
