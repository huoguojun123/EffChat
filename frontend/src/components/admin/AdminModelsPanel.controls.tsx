import type { ReactNode } from "react"
import type { ModelTestResponse } from "@/api/admin"
import type { Model } from "@/types"
import { formatDuration } from "./AdminModelsPanel.helpers"

export function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <label className="block [&_input]:h-8 [&_select]:h-8">
      <span className="mb-1.5 block text-sm font-medium">{label}</span>
      {children}
    </label>
  )
}

export function Select({ value, onChange, children }: { value: string; onChange: (value: string) => void; children: ReactNode }) {
  return (
    <select className="h-8 w-full rounded-md border border-input bg-background px-3 text-sm outline-none focus:ring-1 focus:ring-ring" value={value} onChange={(e) => onChange(e.target.value)}>
      {children}
    </select>
  )
}

export function Toggle({ label, checked, onChange }: { label: string; checked: boolean; onChange: (checked: boolean) => void }) {
  return (
    <button type="button" onClick={() => onChange(!checked)} className={`rounded-md border px-3 py-2 text-sm transition-colors motion-control ${checked ? "border-foreground bg-foreground text-background" : "border-border/70 hover:bg-muted"}`}>
      {label}：{checked ? "开" : "关"}
    </button>
  )
}

export function ModelTestStatus({ result }: { result: ModelTestResponse }) {
  const tone = result.ok
    ? "border-emerald-200 bg-emerald-50 text-emerald-800 dark:border-emerald-500/30 dark:bg-emerald-500/10 dark:text-emerald-200"
    : "border-rose-200 bg-rose-50 text-rose-800 dark:border-rose-500/30 dark:bg-rose-500/10 dark:text-rose-200"
  const unexpectedOutput = !result.ok && result.code === "model_probe_unexpected_output"
  const detail = result.ok ? result.output || "OK" : result.error || "检测失败"
  return (
    <div className={`flex flex-col gap-1 rounded-md border px-3 py-2 text-sm ${tone}`}>
      <div className="flex flex-wrap items-center gap-2 font-medium">
        <span>{result.ok ? "最小对话连通" : unexpectedOutput ? "响应不符合探测要求" : "连通失败"}</span>
        <span className="rounded-full bg-background/70 px-2 py-0.5 text-xs text-foreground/70">{result.provider}</span>
        <span className="min-w-0 truncate text-xs font-normal text-foreground/65">{result.model_id}</span>
        {typeof result.duration_ms === "number" ? <span className="text-xs font-normal text-foreground/65">{formatDuration(result.duration_ms)}</span> : null}
      </div>
      <div className="line-clamp-2 text-sm text-foreground/80">{detail}</div>
      {unexpectedOutput ? <div className="line-clamp-2 text-xs text-foreground/70">模型返回：{result.output || "空文本"}</div> : null}
    </div>
  )
}

export function SwitchButton({ checked, onClick, disabled }: { checked: boolean; onClick: () => void; disabled?: boolean }) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      className={`h-7 rounded-full border px-3 text-xs transition-colors motion-control ${
        checked
          ? "border-emerald-600 bg-emerald-50 text-emerald-700"
          : "border-rose-300 bg-rose-50 text-rose-700"
      }`}
    >
      {checked ? "已启用" : "已停用"}
    </button>
  )
}

export function CapabilityDots({ model }: { model: Model }) {
  const items = [
    model.vision ? "视觉" : "",
    model.tool_use ? "工具" : "",
    model.reasoning ? "推理" : "",
  ].filter(Boolean)
  return <span className="truncate text-xs text-muted-foreground">{items.join(" / ") || "-"}</span>
}
