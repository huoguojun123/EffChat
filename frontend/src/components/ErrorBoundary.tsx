import { Component, type ErrorInfo, type ReactNode } from "react"
import { Button } from "@/components/ui/button"

interface ErrorBoundaryState {
  error: Error | null
}

interface ErrorBoundaryProps {
  children: ReactNode
  fallback?: (error: Error) => ReactNode
}

export class ErrorBoundary extends Component<ErrorBoundaryProps, ErrorBoundaryState> {
  state: ErrorBoundaryState = { error: null }

  static getDerivedStateFromError(error: Error): ErrorBoundaryState {
    return { error }
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error("App render error:", error, info)
  }

  render() {
    if (!this.state.error) return this.props.children
    if (this.props.fallback) return this.props.fallback(this.state.error)

    return (
      <div className="flex h-dvh items-center justify-center bg-background px-4 text-foreground">
        <div className="max-w-sm text-center">
          <h1 className="text-base font-semibold">页面出错了</h1>
          <p className="mt-2 text-sm text-muted-foreground">
            {this.state.error.message || "前端渲染异常，请刷新后继续。"}
          </p>
          <Button className="mt-4" size="sm" onClick={() => window.location.reload()}>
            刷新页面
          </Button>
        </div>
      </div>
    )
  }
}
