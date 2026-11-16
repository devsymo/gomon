import DOMPurify from "dompurify"
import { useMemo } from "react"
import type { Color } from "@/lib/api"
import { colorLabel } from "@/lib/format"
import { cn } from "@/lib/utils"

const dotClass: Record<Color, string> = {
  green: "bg-status-green",
  yellow: "bg-status-yellow",
  red: "bg-status-red",
  purple: "bg-status-purple",
  blue: "bg-status-blue",
  clear: "bg-status-clear",
}

const textClass: Record<Color, string> = {
  green: "text-status-green",
  yellow: "text-status-yellow",
  red: "text-status-red",
  purple: "text-status-purple",
  blue: "text-status-blue",
  clear: "text-muted-foreground",
}

export function StatusDot({ color, className, pulse }: { color: Color; className?: string; pulse?: boolean }) {
  return (
    <span className={cn("relative inline-flex size-2.5 shrink-0", className)} title={colorLabel[color]}>
      {pulse && (color === "red" || color === "yellow") && (
        <span className={cn("absolute inline-flex h-full w-full animate-ping rounded-full opacity-50", dotClass[color])} />
      )}
      <span className={cn("relative inline-flex h-full w-full rounded-full", dotClass[color])} />
    </span>
  )
}

export function StatusBadge({ color, className }: { color: Color; className?: string }) {
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1.5 rounded-md border px-2 py-0.5 text-xs font-medium capitalize",
        textClass[color],
        className
      )}
    >
      <StatusDot color={color} className="size-2" />
      {color}
    </span>
  )
}

export function statusText(color: Color) {
  return textClass[color]
}

const COLOR_TOKEN = /&(green|yellow|red|purple|blue|clear)\b/g

function dotHTML(c: string) {
  return `<span class="inline-block size-2.5 rounded-full align-middle mr-1 bg-status-${c}" title="${c}"></span>`
}

function escapeHTML(s: string) {
  return s.replace(/[&<>"']/g, (ch) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[ch]!)
}

/**
 * Renders a Xymon status message: "&red"-style color tokens become dots.
 * Plugins often emit HTML; it is sanitized to a small allowlist.
 */
export function StatusMessage({ text, className }: { text: string; className?: string }) {
  const html = useMemo(() => {
    const isHTML = /<\s*(table|tr|td|br|p|b|i|pre|font|div|span|a|h[1-6]|ul|li|hr)\b/i.test(text)
    if (isHTML) {
      const withDots = text.replace(COLOR_TOKEN, (_, c) => dotHTML(c))
      return DOMPurify.sanitize(withDots, {
        ALLOWED_TAGS: ["table", "thead", "tbody", "tr", "td", "th", "br", "p", "b", "strong", "i", "em", "u", "pre", "code", "font", "div", "span", "a", "h1", "h2", "h3", "h4", "h5", "h6", "ul", "ol", "li", "hr", "small", "center"],
        ALLOWED_ATTR: ["href", "colspan", "rowspan", "align", "class", "title"],
        ALLOWED_URI_REGEXP: /^(?:https?|mailto):/i,
      })
    }
    // Split on color tokens first so escaping cannot break them.
    return text
      .split(/(&(?:green|yellow|red|purple|blue|clear)\b)/g)
      .map((part) => (/^&(green|yellow|red|purple|blue|clear)$/.test(part) ? dotHTML(part.slice(1)) : escapeHTML(part)))
      .join("")
  }, [text])
  const isHTML = /<\s*(table|tr|td|br|p|div)\b/i.test(text)
  return (
    <div
      className={cn(
        "status-message overflow-x-auto font-mono text-[12.5px] leading-relaxed",
        isHTML ? "whitespace-pre-wrap" : "whitespace-pre",
        className
      )}
      dangerouslySetInnerHTML={{ __html: html }}
    />
  )
}
