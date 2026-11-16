import type { ReactNode } from "react"
import { cn } from "@/lib/utils"

export function Page({ children, className }: { children: ReactNode; className?: string }) {
  return <div className={cn("mx-auto w-full max-w-screen-2xl space-y-6 p-4 lg:p-6", className)}>{children}</div>
}

export function PageHeader({ title, description, actions }: { title: ReactNode; description?: ReactNode; actions?: ReactNode }) {
  return (
    <div className="flex flex-col gap-3 sm:flex-row sm:items-end sm:justify-between">
      <div className="min-w-0">
        <h2 className="truncate text-2xl font-semibold tracking-tight">{title}</h2>
        {description && <p className="text-muted-foreground mt-1 text-sm">{description}</p>}
      </div>
      {actions && <div className="flex shrink-0 flex-wrap items-center gap-2">{actions}</div>}
    </div>
  )
}

export function Empty({ icon, title, children }: { icon?: ReactNode; title: string; children?: ReactNode }) {
  return (
    <div className="text-muted-foreground flex flex-col items-center justify-center gap-2 py-12 text-center">
      {icon}
      <div className="text-foreground font-medium">{title}</div>
      {children && <div className="max-w-md text-sm">{children}</div>}
    </div>
  )
}

export function Field({ label, hint, children, className }: { label: string; hint?: ReactNode; children: ReactNode; className?: string }) {
  return (
    <div className={cn("grid gap-1.5", className)}>
      <label className="text-sm font-medium">{label}</label>
      {children}
      {hint && <p className="text-muted-foreground text-xs">{hint}</p>}
    </div>
  )
}

export function CodeBlock({ children, className }: { children: string; className?: string }) {
  return (
    <pre className={cn("bg-muted overflow-x-auto rounded-md p-3 font-mono text-xs leading-relaxed", className)}>{children}</pre>
  )
}
