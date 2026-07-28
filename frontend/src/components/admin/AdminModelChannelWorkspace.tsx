import type { ReactNode } from "react"
import { Button } from "@/components/ui/button"
import { ArrowLeft } from "lucide-react"

interface AdminModelChannelWorkspaceProps {
  visible: boolean
  title: string
  onBackToChannels: () => void
  children: ReactNode
}

export function AdminModelChannelWorkspace({ visible, title, onBackToChannels, children }: AdminModelChannelWorkspaceProps) {
  return (
    <section className={`min-h-0 flex-col overflow-hidden lg:flex ${visible ? "flex" : "hidden lg:flex"}`}>
      <div className="flex shrink-0 items-center gap-2 border-b border-border/70 px-3 py-2.5 lg:hidden">
        <Button variant="ghost" size="sm" className="h-8 px-2" onClick={onBackToChannels}>
          <ArrowLeft className="h-3.5 w-3.5" />
          渠道
        </Button>
        <div className="min-w-0 truncate text-sm font-medium">{title}</div>
      </div>
      {children}
    </section>
  )
}
