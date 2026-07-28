import type { ReactElement } from "react"
import { Activity, Cpu, FileText, Layers, PlugZap, Puzzle, ScrollText, Server, Settings2, Type, Users, Wrench } from "lucide-react"

export type AdminTabKey = "channels" | "users" | "groups" | "models" | "tools" | "usage" | "systemPrompt" | "config" | "fonts" | "prompts" | "skills" | "status"

type AdminGroupKey = "modelService" | "governance" | "promptKnowledge" | "system"

export type AdminTabItem = {
  key: AdminTabKey
  label: string
  icon: ReactElement
}

export type AdminNavGroup = {
  key: AdminGroupKey
  label: string
  tabs: AdminTabItem[]
}

export const ADMIN_NAV: AdminNavGroup[] = [
  {
    key: "modelService",
    label: "模型与服务",
    tabs: [
      { key: "models", label: "模型", icon: <Cpu className="h-4 w-4" /> },
      { key: "channels", label: "渠道与联网服务", icon: <PlugZap className="h-4 w-4" /> },
    ],
  },
  {
    key: "governance",
    label: "治理与用量",
    tabs: [
      { key: "usage", label: "用量", icon: <Activity className="h-4 w-4" /> },
      { key: "groups", label: "用户组", icon: <Layers className="h-4 w-4" /> },
      { key: "users", label: "用户", icon: <Users className="h-4 w-4" /> },
      { key: "tools", label: "工具", icon: <Wrench className="h-4 w-4" /> },
    ],
  },
  {
    key: "promptKnowledge",
    label: "提示与知识",
    tabs: [
      { key: "systemPrompt", label: "底层提示词", icon: <ScrollText className="h-4 w-4" /> },
      { key: "prompts", label: "提示词库", icon: <FileText className="h-4 w-4" /> },
      { key: "skills", label: "Skills", icon: <Puzzle className="h-4 w-4" /> },
    ],
  },
  {
    key: "system",
    label: "系统",
    tabs: [
      { key: "status", label: "实例状态", icon: <Server className="h-4 w-4" /> },
      { key: "config", label: "系统配置", icon: <Settings2 className="h-4 w-4" /> },
      { key: "fonts", label: "字体", icon: <Type className="h-4 w-4" /> },
    ],
  },
]

export const ADMIN_TABS = ADMIN_NAV.flatMap((group) => group.tabs)

export function isAdminTabKey(value: string | undefined): value is AdminTabKey {
  return Boolean(value && ADMIN_TABS.some((item) => item.key === value))
}

export function adminTab(tab: AdminTabKey) {
  return ADMIN_TABS.find((item) => item.key === tab) || ADMIN_TABS[0]
}

export function isConfigTab(tab: AdminTabKey) {
  return tab === "config" || tab === "systemPrompt"
}
