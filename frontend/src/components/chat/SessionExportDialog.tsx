import { useState } from "react"
import { Download, Ellipsis } from "lucide-react"
import { exportSessionMarkdown } from "@/api/sessions"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { chatSurfaceControlClass } from "./ChatInput.constants"

export function SessionExportDialog({ sessionId }: { sessionId: number }) {
  const [menuOpen, setMenuOpen] = useState(false)
  const [dialogOpen, setDialogOpen] = useState(false)
  const [includeTools, setIncludeTools] = useState(false)
  const [exporting, setExporting] = useState(false)
  const [error, setError] = useState("")

  async function handleExport() {
    setExporting(true)
    setError("")
    try {
      await exportSessionMarkdown(sessionId, includeTools)
      setDialogOpen(false)
    } catch (err) {
      setError(err instanceof Error ? err.message : "导出失败")
    } finally {
      setExporting(false)
    }
  }

  return (
    <>
      <Popover open={menuOpen} onOpenChange={setMenuOpen}>
        <PopoverTrigger asChild>
          <Button
            variant="outline"
            size="icon"
            className={`pointer-events-auto h-8 w-8 ${chatSurfaceControlClass}`}
            aria-label="更多会话操作"
          >
            <Ellipsis className="h-3.5 w-3.5" />
          </Button>
        </PopoverTrigger>
        <PopoverContent align="end" className="w-44 p-1.5">
          <button
            type="button"
            className="flex h-8 w-full items-center gap-2 rounded-md px-2.5 text-left text-sm text-foreground transition-colors hover:bg-accent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
            onClick={() => {
              setMenuOpen(false)
              setDialogOpen(true)
              setError("")
            }}
          >
            <Download className="h-3.5 w-3.5" />
            导出 Markdown
          </button>
        </PopoverContent>
      </Popover>

      <Dialog open={dialogOpen} onOpenChange={(open) => !exporting && setDialogOpen(open)}>
        <DialogContent className="max-w-sm">
          <DialogHeader>
            <DialogTitle>导出 Markdown</DialogTitle>
            <DialogDescription>导出当前会话的用户消息和选中回答。</DialogDescription>
          </DialogHeader>
          <label className="flex cursor-pointer items-center gap-2.5 rounded-md border border-border/70 px-3 py-2.5 text-sm text-foreground">
            <input
              type="checkbox"
              className="h-4 w-4 accent-primary"
              checked={includeTools}
              onChange={(event) => setIncludeTools(event.target.checked)}
              disabled={exporting}
            />
            包含工具摘要
          </label>
          {error ? <p role="alert" className="text-sm text-destructive">{error}</p> : null}
          <DialogFooter>
            <Button variant="ghost" size="sm" onClick={() => setDialogOpen(false)} disabled={exporting}>取消</Button>
            <Button size="sm" onClick={handleExport} disabled={exporting}>{exporting ? "正在导出" : "下载"}</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  )
}
