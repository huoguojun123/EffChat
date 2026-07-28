import { useState } from "react"
import { cn } from "@/lib/utils"

interface UserAvatarProps {
  src?: string | null
  name: string
  className?: string
}

export function UserAvatar({ src, name, className }: UserAvatarProps) {
  const [failedSrc, setFailedSrc] = useState<string | null>(null)
  const failed = !!src && failedSrc === src

  return (
    <span
      className={cn(
        "inline-flex shrink-0 items-center justify-center overflow-hidden bg-sidebar-accent font-medium text-sidebar-foreground",
        className
      )}
    >
      {src && !failed ? (
        <img
          src={src}
          alt=""
          className="h-full w-full object-cover"
          onError={() => setFailedSrc(src || null)}
        />
      ) : (
        (name || "U")[0].toUpperCase()
      )}
    </span>
  )
}
