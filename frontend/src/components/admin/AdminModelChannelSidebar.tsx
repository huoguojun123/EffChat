import type { Model } from "@/types"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Plus, Search } from "lucide-react"
import { providerLabels } from "./AdminModelsPanel.constants"

interface AdminModelChannelSidebarProps {
  visible: boolean
  grouped: Array<{ provider: string; items: Model[] }>
  selectedProvider: string
  unconfiguredProviderKey: string
  channelLabels: Record<string, string>
  query: string
  onQueryChange: (query: string) => void
  onSelectProvider: (provider: string) => void
  onCreateChannel: () => void
}

export function AdminModelChannelSidebar({
  visible,
  grouped,
  selectedProvider,
  unconfiguredProviderKey,
  channelLabels,
  query,
  onQueryChange,
  onSelectProvider,
  onCreateChannel,
}: AdminModelChannelSidebarProps) {
  return (
    <aside aria-label="模型渠道" className={`min-h-0 flex-col border-b border-border/70 lg:flex lg:border-b-0 lg:border-r ${visible ? "flex" : "hidden lg:flex"}`}>
      <div className="flex items-center justify-between border-b border-border/70 px-3 py-3">
        <div className="text-sm font-medium">渠道</div>
        <Button type="button" size="sm" onClick={onCreateChannel}>
          <Plus className="h-3.5 w-3.5" />
          新建
        </Button>
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto p-2">
        {grouped.map(({ provider, items }) => {
          const enabledCount = items.filter((item) => item.enabled).length
          const isMissingGroup = provider === unconfiguredProviderKey
          return (
            <button
              key={provider}
              onClick={() => onSelectProvider(provider)}
              aria-pressed={selectedProvider === provider}
              className={`mb-1 flex w-full items-center justify-between rounded-lg px-3 py-2 text-left text-sm outline-none transition-colors motion-control focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring/50 ${
                selectedProvider === provider
                  ? "bg-accent/80 text-accent-foreground"
                  : isMissingGroup
                    ? "text-rose-700 hover:bg-rose-500/10 dark:text-rose-300"
                    : "text-muted-foreground hover:bg-muted/60 hover:text-foreground"
              }`}
            >
              <span className="min-w-0 truncate">{channelLabels[provider] || providerLabels[provider] || provider}</span>
              <span className="shrink-0 text-xs opacity-70">{enabledCount}/{items.length}</span>
            </button>
          )
        })}
      </div>
      <div className="border-t border-border/70 p-2">
        <div className="relative">
          <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
          <Input type="search" name="effchat-model-search" autoComplete="off" autoCorrect="off" spellCheck={false} value={query} onChange={(e) => onQueryChange(e.target.value)} className="h-8 pl-8 text-sm" placeholder="搜索模型" aria-label="搜索模型" />
        </div>
      </div>
    </aside>
  )
}
