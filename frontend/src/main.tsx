import { StrictMode } from "react"
import { createRoot } from "react-dom/client"
import App from "./App"
import { TooltipProvider } from "@/components/ui/tooltip"
import "katex/dist/katex.min.css"
import "./globals.css"

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <TooltipProvider delayDuration={500} skipDelayDuration={200}>
      <App />
    </TooltipProvider>
  </StrictMode>
)
