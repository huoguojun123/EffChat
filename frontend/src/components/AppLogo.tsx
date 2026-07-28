import { cn } from "@/lib/utils"

export function AppLogo({ className }: { className?: string }) {
  return (
    <svg
      viewBox="0 0 64 64"
      role="img"
      aria-label="EffChat"
      className={cn("h-6 w-6 text-foreground", className)}
      fill="none"
      xmlns="http://www.w3.org/2000/svg"
    >
      <rect x="7" y="7" width="50" height="50" rx="16" fill="currentColor" />
      <path
        d="M20 24.5C20 21.46 22.46 19 25.5 19H39C42.31 19 45 21.69 45 25V34.5C45 37.54 42.54 40 39.5 40H33.2L25.7 46V40H25.5C22.46 40 20 37.54 20 34.5V24.5Z"
        fill="var(--bg)"
      />
      <path d="M26 28H38" stroke="currentColor" strokeWidth="3" strokeLinecap="round" />
      <path d="M26 34H34" stroke="currentColor" strokeWidth="3" strokeLinecap="round" />
      <path d="M42 18L47 13" stroke="currentColor" strokeWidth="3" strokeLinecap="round" />
      <path d="M46 23H52" stroke="currentColor" strokeWidth="3" strokeLinecap="round" />
    </svg>
  )
}
