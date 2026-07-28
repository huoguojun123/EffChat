import { useEffect, useState, type ReactNode } from "react"
import { Box, Database, HardDrive, Server } from "lucide-react"
import { adminApi, type AdminSystemStatus } from "@/api/admin"
import { LoadingIndicator } from "@/components/ui/loading-indicator"
import { formatBytes } from "@/lib/format"

interface Props {
  refreshSignal: number
  setError: (error: string) => void
}

export function AdminStatusPanel({ refreshSignal, setError }: Props) {
  const [status, setStatus] = useState<AdminSystemStatus | null>(null)
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    let canceled = false
    async function load() {
      setLoading(true)
      setError("")
      try {
        const response = await adminApi.getSystemStatus()
        if (!canceled) setStatus(response)
      } catch (error) {
        if (!canceled) setError(error instanceof Error ? error.message : "实例状态加载失败")
      } finally {
        if (!canceled) setLoading(false)
      }
    }
    void load()
    return () => {
      canceled = true
    }
  }, [refreshSignal, setError])

  if (loading && !status) return <LoadingIndicator label="正在读取实例状态" className="h-full min-h-40" />
  if (!status) return null

  const storageUsed = Math.max(0, status.storage.total_bytes - status.storage.free_bytes)
  const containerMemory =
    status.runtime.container_memory_used_bytes !== undefined
      ? status.runtime.container_memory_limit_bytes
        ? `${formatBytes(status.runtime.container_memory_used_bytes)} / ${formatBytes(status.runtime.container_memory_limit_bytes)}`
        : formatBytes(status.runtime.container_memory_used_bytes)
      : "不可用"

  return (
    <div className="h-full min-h-0 overflow-y-auto pb-8">
      <div className="border-b border-border/70 pb-4">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div>
            <h2 className="text-sm font-semibold">部署实例</h2>
            <p className="mt-1 text-sm text-muted-foreground">当前应用容器可见的运行状态。</p>
          </div>
          <span className="rounded-md border border-emerald-600/25 bg-emerald-500/8 px-2 py-1 text-xs font-medium text-emerald-700 dark:text-emerald-400">运行中</span>
        </div>
        <dl className="mt-4 grid gap-x-8 gap-y-3 text-sm sm:grid-cols-2 xl:grid-cols-4">
          <StatusValue label="版本" value={status.version} />
          <StatusValue label="构建" value={status.build_ref || "unknown"} mono />
          <StatusValue label="数据库结构" value={status.schema_version || "不可用"} mono />
          <StatusValue label="运行时间" value={formatUptime(status.uptime_seconds)} />
        </dl>
      </div>

      <div className="divide-y divide-border/70">
        <StatusSection icon={<Server className="h-4 w-4" />} title="运行时">
          <StatusValue label="Go" value={status.runtime.go_version} mono />
          <StatusValue label="CPU" value={`${status.runtime.cpu_count} 核`} />
          <StatusValue label="Goroutine" value={formatNumber(status.runtime.goroutines)} />
          <StatusValue label="Go 堆内存" value={formatBytes(status.runtime.heap_alloc_bytes)} />
        </StatusSection>

        <StatusSection icon={<Box className="h-4 w-4" />} title="容器">
          <StatusValue label="内存" value={containerMemory} />
          <StatusValue label="启动时间" value={formatDateTime(status.started_at)} />
        </StatusSection>

        <StatusSection icon={<HardDrive className="h-4 w-4" />} title="受管存储">
          <StatusValue label="已用" value={status.storage.total_bytes ? formatBytes(storageUsed) : "不可用"} />
          <StatusValue label="可用" value={status.storage.total_bytes ? formatBytes(status.storage.free_bytes) : "不可用"} />
          <StatusValue label="总量" value={status.storage.total_bytes ? formatBytes(status.storage.total_bytes) : "不可用"} />
        </StatusSection>

        <StatusSection icon={<Database className="h-4 w-4" />} title="依赖服务">
          <StatusValue label="PostgreSQL" value={status.database.ok ? `正常 · ${status.database.latency_ms}ms` : "不可用"} healthy={status.database.ok} />
          <StatusValue label="连接池" value={`${status.database.in_use_connections} 使用中 · ${status.database.idle_connections} 空闲 · ${status.database.open_connections} 总计`} />
          <StatusValue
            label="文档提取器"
            value={!status.extractor.enabled ? "未启用" : status.extractor.ok ? `正常 · ${status.extractor.latency_ms}ms` : "不可用"}
            healthy={status.extractor.enabled ? status.extractor.ok : undefined}
          />
        </StatusSection>
      </div>

      {loading ? <div className="pt-3 text-xs text-muted-foreground">正在刷新…</div> : null}
    </div>
  )
}

function StatusSection({ icon, title, children }: { icon: ReactNode; title: string; children: ReactNode }) {
  return (
    <section className="grid gap-4 py-5 lg:grid-cols-[180px_minmax(0,1fr)]">
      <h3 className="flex items-center gap-2 text-sm font-medium text-foreground/85">
        {icon}
        {title}
      </h3>
      <dl className="grid gap-x-8 gap-y-3 text-sm sm:grid-cols-2 xl:grid-cols-3">{children}</dl>
    </section>
  )
}

function StatusValue({ label, value, mono = false, healthy }: { label: string; value: string; mono?: boolean; healthy?: boolean }) {
  return (
    <div className="min-w-0">
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd
        className={`mt-0.5 break-words font-medium ${mono ? "font-mono text-xs" : "text-sm"} ${healthy === false ? "text-destructive" : healthy === true ? "text-emerald-700 dark:text-emerald-400" : "text-foreground"}`}
      >
        {value}
      </dd>
    </div>
  )
}

function formatNumber(value: number) {
  return new Intl.NumberFormat("zh-CN").format(value)
}

function formatDateTime(value: string) {
  const date = new Date(value)
  return Number.isNaN(date.getTime())
    ? "不可用"
    : new Intl.DateTimeFormat("zh-CN", {
        dateStyle: "medium",
        timeStyle: "medium"
      }).format(date)
}

function formatUptime(seconds: number) {
  const days = Math.floor(seconds / 86400)
  const hours = Math.floor((seconds % 86400) / 3600)
  const minutes = Math.floor((seconds % 3600) / 60)
  if (days) return `${days} 天 ${hours} 小时`
  if (hours) return `${hours} 小时 ${minutes} 分钟`
  return `${minutes} 分钟`
}
