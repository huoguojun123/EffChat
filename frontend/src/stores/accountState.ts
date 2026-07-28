import { clearDiagramRenderCache } from "@/components/workspace/diagramRenderCache"
import { clearAccountScopedStorage } from "@/lib/accountStorage"
import { useChatStore } from "./chat"
import { useModelStore } from "./models"
import { useSkillStore } from "./skills"
import { useUIStore } from "./ui"

export function clearAccountScopedState() {
  clearAccountScopedStorage()
  clearDiagramRenderCache()
  useChatStore.getState().resetAccountState()
  useModelStore.getState().resetAccountState()
  useSkillStore.getState().resetAccountState()
  useUIStore.getState().resetAccountState()
}
