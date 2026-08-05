import type { Dispatch, SetStateAction } from "react"
import type { AIChannel, CreateModelInput } from "@/api/admin"
import type { Model, UserGroup } from "@/types"

export interface AdminModelsPanelProps {
  models: Model[]
  setModels: Dispatch<SetStateAction<Model[]>>
  groups: UserGroup[]
  channels?: AIChannel[]
  setChannels?: Dispatch<SetStateAction<AIChannel[]>>
  setError: (error: string) => void
  onDirtyChange?: (dirty: boolean) => void
}

export type ModelDraft = CreateModelInput

export interface GroupLevelOption {
  level: number
  label: string
}
