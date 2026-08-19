import type { ChangeEvent, RefObject } from "react"
import { Brain, Check, ChevronLeft, ChevronRight, Globe, Loader2, MoreHorizontal, Paperclip, ScrollText, Sparkles } from "lucide-react"
import type { Model, Session, SkillDefinition, ThinkingEffortOption } from "@/types"
import { Button } from "@/components/ui/button"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { MotionDrill } from "@/components/ui/motion"
import { cn } from "@/lib/utils"
import { ACCEPT_ATTR, SEARCH_MODE_LABEL, composerIconButtonClass, motionIndex, type SearchMode } from "./ChatInput.constants"
import { MenuItem, SkillMenuItem } from "./ChatInputParts"

interface ChatInputToolbarProps {
  fileInputRef: RefObject<HTMLInputElement | null>
  uploading: boolean
  activeSessionId: number | null
  activeSession?: Session
  currentModel?: Model
  searchMode: SearchMode
  setSearchMode: (mode: SearchMode) => void
  thinkingMenuOpen: boolean
  setThinkingMenuOpen: (open: boolean) => void
  thinkingActive: boolean
  thinkingLabel: string
  thinkingOptions: ThinkingEffortOption[]
  thinkingEffort: string
  setThinkingEffort: (value: string) => void
  menuOpen: boolean
  setMenuOpen: (open: boolean) => void
  menuView: "main" | "skills"
  setMenuView: (view: "main" | "skills") => void
  memoryEnabled: boolean
  activeSkillCount: number
  skills: SkillDefinition[]
  enabledSkillIds: string[]
  compacting: boolean
  onFileSelect: (event: ChangeEvent<HTMLInputElement>) => void
  onOpenStaging: () => void
  onPromptPickerOpen: () => void
  onOpenMemoryManager: () => void
  onToggleSkill: (skillId: string) => void
  onCompact: () => void
  memoryUnseen?: boolean
  className?: string
}

export function ChatInputToolbar({
  fileInputRef,
  uploading,
  activeSessionId,
  activeSession,
  searchMode,
  setSearchMode,
  thinkingMenuOpen,
  setThinkingMenuOpen,
  thinkingActive,
  thinkingLabel,
  thinkingOptions,
  thinkingEffort,
  setThinkingEffort,
  menuOpen,
  setMenuOpen,
  menuView,
  setMenuView,
  memoryEnabled,
  activeSkillCount,
  skills,
  enabledSkillIds,
  compacting,
  onFileSelect,
  onOpenStaging,
  onPromptPickerOpen,
  onOpenMemoryManager,
  onToggleSkill,
  onCompact,
  memoryUnseen = false,
  className,
}: ChatInputToolbarProps) {
  const searchEnabled = searchMode !== "off"

  return (
    <div data-testid="composer-toolbar" className={cn("flex items-center gap-0 rounded-[13px] border border-border/70 bg-popover/96 p-0.5 shadow-[0_8px_24px_-18px_rgba(0,0,0,0.28),0_1px_4px_rgba(0,0,0,0.05)] sm:gap-0.5", className)}>
      <input
        ref={fileInputRef}
        type="file"
        multiple
        accept={ACCEPT_ATTR}
        className="hidden"
        onChange={onFileSelect}
        data-testid="file-input"
      />

      <Button
        size="icon"
        variant="ghost"
        className={composerIconButtonClass}
        onClick={onOpenStaging}
        disabled={!activeSessionId}
        title="暂存附件"
        aria-label="暂存附件"
      >
        {uploading ? <Loader2 className="h-4 w-4 animate-spin motion-reduce:animate-none" aria-hidden="true" /> : <Paperclip className="h-4 w-4" aria-hidden="true" />}
      </Button>

      <Button
        size="icon"
        variant="ghost"
        className={cn(
          composerIconButtonClass,
          searchEnabled && "bg-sky-100/80 text-sky-700 hover:bg-sky-100 dark:bg-sky-500/15 dark:text-sky-300 dark:hover:bg-sky-500/25"
        )}
        disabled={!activeSessionId}
        title={SEARCH_MODE_LABEL[searchMode]}
        aria-label={SEARCH_MODE_LABEL[searchMode]}
        aria-pressed={searchEnabled}
        onClick={() => setSearchMode(searchEnabled ? "off" : "auto")}
      >
        <Globe className="h-4 w-4" aria-hidden="true" />
      </Button>

      <Popover open={thinkingMenuOpen} onOpenChange={setThinkingMenuOpen}>
        <PopoverTrigger asChild>
          <Button
            size="icon"
            variant="ghost"
            className={cn(
              composerIconButtonClass,
              thinkingActive && "bg-amber-100/80 text-amber-700 hover:bg-amber-100 dark:bg-amber-500/15 dark:text-amber-300 dark:hover:bg-amber-500/25"
            )}
            disabled={!activeSessionId || !thinkingActive}
            title={thinkingActive ? `思考强度：${thinkingLabel}` : "当前模型不支持思考强度"}
            aria-label={thinkingActive ? `思考强度：${thinkingLabel}` : "当前模型不支持思考强度"}
          >
            <Brain className="h-4 w-4" aria-hidden="true" />
          </Button>
        </PopoverTrigger>
        <PopoverContent align="start" side="top" className="w-[min(18rem,calc(100vw-1.5rem))] p-1.5">
          <div className="px-1.5 pb-1.5 pt-1 text-xs font-medium text-muted-foreground">思考强度</div>
          {thinkingOptions.map((option, index) => (
            <div key={option.value} className="motion-stagger-item" style={motionIndex(index)}>
              <MenuItem
                icon={<Brain className="h-4 w-4" />}
                label={option.label}
                hint={option.desc}
                active={thinkingEffort === option.value}
                trailing={thinkingEffort === option.value ? <Check className="h-4 w-4 text-primary" /> : undefined}
                onClick={() => {
                  setThinkingEffort(option.value)
                  setThinkingMenuOpen(false)
                }}
              />
            </div>
          ))}
        </PopoverContent>
      </Popover>

      <Popover
        open={menuOpen}
        onOpenChange={(open) => {
          setMenuOpen(open)
          if (!open) setMenuView("main")
        }}
      >
        <PopoverTrigger asChild>
          <Button
            size="icon"
            variant="ghost"
            className={cn(
              composerIconButtonClass,
              activeSession?.system_prompt || memoryEnabled || activeSkillCount > 0
                ? "bg-primary/10 text-primary hover:bg-primary/15"
                : ""
            )}
            disabled={!activeSessionId}
            title="更多"
            aria-label="更多"
          >
            <MoreHorizontal className="h-4 w-4" aria-hidden="true" />
          </Button>
        </PopoverTrigger>
        <PopoverContent align="start" side="top" className="w-[min(20rem,calc(100vw-1.5rem))] p-1.5">
          <MotionDrill
            view={menuView === "main" ? "main" : "detail"}
            main={
              <div>
                <div className="motion-stagger-item" style={motionIndex(0)}>
                  <MenuItem
                    icon={<ScrollText className="h-4 w-4" />}
                    label="系统提示词"
                    active={!!activeSession?.system_prompt}
                    onClick={() => {
                      setMenuOpen(false)
                      onPromptPickerOpen()
                    }}
                  />
                </div>
                <div className="motion-stagger-item" style={motionIndex(1)}>
                  <MenuItem
                    icon={
                      <span className="relative flex h-4 w-4 items-center justify-center">
                        <Brain className="h-4 w-4" />
                        {memoryUnseen ? <span className="absolute right-0 top-0 h-1.5 w-1.5 translate-x-1/2 -translate-y-1/2 rounded-full bg-sky-500 ring-2 ring-popover" /> : null}
                      </span>
                    }
                    label="会话记忆"
                    hint={memoryEnabled ? "已开启" : "已关闭"}
                    active={memoryEnabled}
                    trailing={memoryEnabled ? <Check className="h-4 w-4 text-primary" /> : undefined}
                    onClick={() => {
                      setMenuOpen(false)
                      onOpenMemoryManager()
                    }}
                  />
                </div>
                <div className="motion-stagger-item" style={motionIndex(2)}>
                  <MenuItem
                    icon={<Sparkles className="h-4 w-4" />}
                    label="Skills"
                    hint={`${activeSkillCount}/${skills.length}`}
                    active={activeSkillCount > 0}
                    disabled={skills.length === 0}
                    trailing={<ChevronRight className="h-4 w-4 text-muted-foreground" />}
                    onClick={() => setMenuView("skills")}
                  />
                </div>
                <div className="motion-stagger-item" style={motionIndex(3)}>
                  <MenuItem
                    icon={compacting ? <Loader2 className="h-4 w-4 animate-spin" /> : <Sparkles className="h-4 w-4" />}
                    label="压缩历史对话"
                    disabled={compacting || !activeSessionId}
                    onClick={() => {
                      setMenuOpen(false)
                      onCompact()
                    }}
                  />
                </div>
              </div>
            }
            detail={
              <div className="min-h-0">
                <div className="flex h-10 items-center gap-2 border-b border-border/70 px-1 pb-1">
                  <Button variant="ghost" size="icon" className="h-8 w-8 rounded-lg" onClick={() => setMenuView("main")} aria-label="返回更多功能">
                    <ChevronLeft className="h-4 w-4" aria-hidden="true" />
                  </Button>
                  <div className="min-w-0 flex-1 font-medium">Skills</div>
                  <div className="text-sm text-muted-foreground">{activeSkillCount}/{skills.length}</div>
                </div>
                <div className="max-h-[min(56dvh,360px)] overflow-y-auto py-1 scrollbar-thin">
                  {skills.map((skill, index) => (
                    <div key={skill.id} className="motion-stagger-item" style={motionIndex(index)}>
                      <SkillMenuItem
                        skill={skill}
                        active={enabledSkillIds.includes(skill.id)}
                        onClick={() => onToggleSkill(skill.id)}
                      />
                    </div>
                  ))}
                </div>
              </div>
            }
          />
        </PopoverContent>
      </Popover>
    </div>
  )
}
