import { useState } from "react"

interface UseAdminModelSelectionOptions {
  initialProvider: string
  fallbackProvider: string
}

export function useAdminModelSelection({ initialProvider, fallbackProvider }: UseAdminModelSelectionOptions) {
  const [query, setQuery] = useState("")
  const [activeProvider, setActiveProvider] = useState(initialProvider || fallbackProvider)
  const [editingId, setEditingId] = useState("")
  const [creating, setCreating] = useState(false)
  const [mobileWorkspaceOpen, setMobileWorkspaceOpen] = useState(false)
  const [mobileDetailOpen, setMobileDetailOpen] = useState(false)
  const [modelManagementOpen, setModelManagementOpen] = useState(false)

  return {
    query,
    setQuery,
    activeProvider,
    setActiveProvider,
    editingId,
    setEditingId,
    creating,
    setCreating,
    mobileWorkspaceOpen,
    setMobileWorkspaceOpen,
    mobileDetailOpen,
    setMobileDetailOpen,
    modelManagementOpen,
    setModelManagementOpen,
  }
}
