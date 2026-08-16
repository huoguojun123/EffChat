import * as React from "react"
import { cn } from "@/lib/utils"

const Input = React.forwardRef<HTMLInputElement, React.InputHTMLAttributes<HTMLInputElement>>(
  ({ autoComplete, className, type, ...props }, ref) => {
    const credentialField = autoComplete === "username" || autoComplete === "current-password" || autoComplete === "new-password"
    return (
      <input
        type={type}
        autoComplete={autoComplete ?? "off"}
        data-1p-ignore={credentialField ? undefined : "true"}
        data-lpignore={credentialField ? undefined : "true"}
        className={cn(
          "flex h-9 min-h-[34px] w-full rounded-lg border border-input/60 bg-popover px-3 py-1 text-base shadow-sm transition-[border-color,box-shadow,background-color] motion-control file:border-0 file:bg-transparent file:text-sm file:font-medium file:text-foreground placeholder:text-muted-foreground/60 hover:border-input focus-visible:border-ring focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40 disabled:cursor-not-allowed disabled:opacity-50 md:text-sm",
          className
        )}
        ref={ref}
        {...props}
      />
    )
  }
)
Input.displayName = "Input"

export { Input }
