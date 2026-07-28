import { useState } from "react"
import { useNavigate, Link } from "react-router"
import { useAuthStore } from "@/stores/auth"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { AppLogo } from "@/components/AppLogo"
import { useSystemStore } from "@/stores/system"
import { ArrowRight, LoaderCircle } from "lucide-react"

export function LoginPage() {
  const [username, setUsername] = useState("")
  const [password, setPassword] = useState("")
  const [error, setError] = useState("")
  const { login, isLoading } = useAuthStore()
  const systemName = useSystemStore((state) => state.systemName)
  const navigate = useNavigate()

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    setError("")
    try {
      await login(username, password)
      navigate("/")
    } catch (err) {
      setError(err instanceof Error ? err.message : "登录失败")
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
          <h1 className="text-2xl font-semibold leading-tight">欢迎回来</h1>
          <p className="mt-2 text-sm text-muted-foreground">登录后继续你的对话。</p>
        </header>

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
              name="current-password"
              autoComplete="current-password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              className="h-11"
              required
            />
          </div>
          <Button type="submit" className="h-10 w-full" disabled={isLoading}>
            {isLoading ? <LoaderCircle className="animate-spin motion-reduce:animate-none" /> : null}
            {isLoading ? "登录中" : "登录"}
            {!isLoading ? <ArrowRight /> : null}
          </Button>
          <p className="pt-1 text-center text-sm text-muted-foreground">
            没有账号？{" "}
            <Link to="/register" className="font-medium text-primary underline-offset-4 transition-colors hover:underline">
              注册
            </Link>
          </p>
        </form>
      </section>
    </main>
  )
}
