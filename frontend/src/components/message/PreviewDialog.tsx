import { createPortal } from "react-dom"
import { WorkspaceWindow } from "@/components/ui/workspace-window"
import { HtmlPreviewFrame } from "@/components/workspace/HtmlPreviewFrame"
import { MermaidPreview } from "@/components/workspace/MermaidPreview"
import { MindMapPreview } from "@/components/workspace/MindMapPreview"
import { GraphvizPreview } from "@/components/workspace/GraphvizPreview"
import type { PreviewArtifact } from "./previewArtifact"

export type PreviewDialogPhase = "idle" | "preparing" | "open"

interface Props {
  artifact: PreviewArtifact | null
  phase: PreviewDialogPhase
  onReady: () => void
  onError: (message: string) => void
  onClose: () => void
}

export function PreviewDialog({ artifact, phase, onReady, onError, onClose }: Props) {
  if (!artifact || phase === "idle") return null

  if (phase === "preparing") {
    return createPortal(
      <div aria-hidden className="pointer-events-none fixed -left-[120vw] top-0 z-[-1] h-[100dvh] w-screen overflow-hidden opacity-0">
        <PreviewSurface artifact={artifact} onReady={onReady} onError={onError} />
      </div>,
      document.body
    )
  }

  return (
    <WorkspaceWindow
      open
      onOpenChange={(open) => {
        if (!open) onClose()
      }}
      title={artifact.title}
      defaultWidth={1280}
      defaultHeight={840}
    >
      <PreviewSurface artifact={artifact} onReady={onReady} onError={onError} />
    </WorkspaceWindow>
  )
}

function PreviewSurface({ artifact, onReady, onError }: { artifact: PreviewArtifact; onReady: () => void; onError: (message: string) => void }) {
  if (artifact.type === "mermaid") return <MermaidPreview code={artifact.content} fill className="h-full w-full" onReady={onReady} onError={onError} />
  if (artifact.type === "mindmap") return <MindMapPreview code={artifact.content} fill className="h-full w-full" onReady={onReady} onError={onError} />
  if (artifact.type === "graphviz") return <GraphvizPreview code={artifact.content} engine={artifact.language} fill className="h-full w-full" onReady={onReady} onError={onError} />
  if (artifact.type === "html" || artifact.type === "svg") {
    return <HtmlPreviewFrame id={artifact.id} type={artifact.type} content={artifact.content} onReady={onReady} onError={onError} />
  }
  return null
}
