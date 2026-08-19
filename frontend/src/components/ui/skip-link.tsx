interface SkipLinkProps {
  targetId?: string
}

export function SkipLink({ targetId = "main-content" }: SkipLinkProps) {
  return (
    <a
      href={`#${targetId}`}
      className="fixed left-3 top-3 z-[100] -translate-y-[200%] rounded-md bg-foreground px-3 py-2 text-sm text-background shadow-lg transition-transform motion-control focus-visible:translate-y-0 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
    >
      跳到主要内容
    </a>
  )
}
