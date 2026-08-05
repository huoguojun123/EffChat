import type { Dispatch, SetStateAction } from "react"
import type { ExternalService } from "@/api/admin"
import { AdminExternalServiceChain } from "./AdminExternalServiceChain"

interface Props {
  services: ExternalService[]
  setServices: Dispatch<SetStateAction<ExternalService[]>>
  setError: (error: string) => void
  onDirtyChange?: (dirty: boolean) => void
}

export function AdminChannelsPanel({ services, setServices, setError, onDirtyChange }: Props) {
  return (
    <div className="h-full min-h-0 overflow-hidden">
      <AdminExternalServiceChain services={services} setServices={setServices} setError={setError} onDirtyChange={onDirtyChange} />
    </div>
  )
}
