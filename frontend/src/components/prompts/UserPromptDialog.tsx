import { PromptManager } from "@/components/prompts/PromptManager"
import { WorkspaceWindow } from "@/components/ui/workspace-window"

interface Props {
  open: boolean
  onOpenChange: (open: boolean) => void
}

export function UserPromptDialog({ open, onOpenChange }: Props) {
  return (
    <WorkspaceWindow
      open={open}
      onOpenChange={onOpenChange}
      title="提示词管理"
      defaultWidth={1120}
      defaultHeight={780}
    >
      <PromptManager scope="user" />
    </WorkspaceWindow>
  )
}
