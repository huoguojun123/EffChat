import { useState } from "react"
import { Link } from "react-router"
import { useAuthStore } from "@/stores/auth"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { AppLogo } from "@/components/AppLogo"
import { useSystemStore } from "@/stores/system"
import { ArrowRight, Check, LoaderCircle } from "lucide-react"

export function RegisterPage() {
  const [username, setUsername] = useState("")
  const [password, setPassword] = useState("")
  const [confirm, setConfirm] = useState("")
  const [error, setError] = useState("")
  const [success, setSuccess] = useState("")
  const [approved, setApproved] = useState(false)
  const { register, isLoading } = useAuthStore()
  const systemName = useSystemStore((state) => state.systemName)

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    setError("")
    setSuccess("")
    if (password !== confirm) {
      setError("两次密码不一致")
      return
    }
    if (password.length < 6) {
      setError("密码至少 6 位")
      return
    }
    try {
      const result = await register(username, password)
      setApproved(result.approved)
      setSuccess(result.message)
    } catch (err) {
      setError(err instanceof Error ? err.message : "注册失败")
    }
  }

  return (
    <main className="auth-shell flex min-h-dvh items-center justify-center overflow-y-auto overscroll-none px-4 py-[max(1rem,env(safe-area-inset-top))]">
      <section className="auth-surface w-full max-w-[400px] border border-border/60 bg-popover/78 px-6 py-7 shadow-[0_20px_65px_rgba(0,0,0,0.12)] backdrop-blur-xl sm:px-8 sm:py-8">
        <header className="mb-7">
          <div className="mb-7 flex items-center gap-2.5 text-sm font-semibold">
            <AppLogo className="h-7 w-7" />
            <span>{systemName}</span>
          </div>
          <h1 className="text-2xl font-semibold leading-tight">{success ? "账号已创建" : "创建账号"}</h1>
          {!success ? <p className="mt-2 text-sm text-muted-foreground">注册后由管理员审核使用权限。</p> : null}
        </header>

        {success ? (
          <div className="auth-feedback space-y-5">
            <div className="flex gap-3 border-l-2 border-primary bg-primary/6 px-3 py-2.5 text-sm leading-6">
              <Check className="mt-1 h-4 w-4 shrink-0 text-primary" aria-hidden="true" />
              <span>{success}</span>
            </div>
            <Button asChild className="h-10 w-full">
              <Link to={approved ? "/" : "/login"}>
                {approved ? "进入系统" : "返回登录页"}
                <ArrowRight />
              </Link>
            </Button>
          </div>
        ) : (
          <form onSubmit={handleSubmit} className="space-y-4">
            {error && (
              <p role="alert" className="auth-feedback border-l-2 border-destructive bg-destructive/6 px-3 py-2 text-sm text-destructive">
                {error}
              </p>
            )}
            <div className="space-y-2">
              <Label htmlFor="username">账号</Label>
              <Input
                id="username"
                name="username"
                autoComplete="username"
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                className="h-11"
                autoFocus
                required
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="password">密码</Label>
              <Input
                id="password"
                type="password"
                name="new-password"
                autoComplete="new-password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                className="h-11"
                minLength={6}
                required
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="confirm">确认密码</Label>
              <Input
                id="confirm"
                type="password"
                name="confirm-password"
                autoComplete="new-password"
                value={confirm}
                onChange={(e) => setConfirm(e.target.value)}
                className="h-11"
                minLength={6}
                required
              />
            </div>
            <Button type="submit" className="h-10 w-full" disabled={isLoading}>
              {isLoading ? <LoaderCircle className="animate-spin motion-reduce:animate-none" /> : null}
              {isLoading ? "创建中" : "创建账号"}
              {!isLoading ? <ArrowRight /> : null}
            </Button>
            <p className="pt-1 text-center text-sm text-muted-foreground">
              已有账号？{" "}
              <Link to="/login" className="font-medium text-primary underline-offset-4 transition-colors hover:underline">
                登录
              </Link>
            </p>
          </form>
        )}
      </section>
    </main>
  )
}
