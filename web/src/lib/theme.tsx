import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react"
import { useQuery } from "@tanstack/react-query"
import { api, type ThemeDef } from "./api"

export type Mode = "light" | "dark" | "system"

interface ThemeCtx {
  themes: ThemeDef[]
  themeId: string
  setThemeId: (id: string) => void
  mode: Mode
  setMode: (m: Mode) => void
  resolvedDark: boolean
}

const Ctx = createContext<ThemeCtx | null>(null)

function read(key: string, def: string) {
  try {
    return localStorage.getItem(key) ?? def
  } catch {
    return def
  }
}

function write(key: string, value: string) {
  try {
    localStorage.setItem(key, value)
  } catch {
    /* storage unavailable */
  }
}

function useSystemDark() {
  const mq = typeof window !== "undefined" ? window.matchMedia("(prefers-color-scheme: dark)") : null
  const [dark, setDark] = useState(mq?.matches ?? false)
  useEffect(() => {
    if (!mq) return
    const fn = (e: MediaQueryListEvent) => setDark(e.matches)
    mq.addEventListener("change", fn)
    return () => mq.removeEventListener("change", fn)
  }, [mq])
  return dark
}

export function ThemeProvider({ children }: { children: ReactNode }) {
  const { data: themes = [] } = useQuery({ queryKey: ["themes"], queryFn: () => api.get<ThemeDef[]>("/themes"), staleTime: Infinity })
  const [themeId, setThemeIdState] = useState(() => read("gomon-theme", "minimal"))
  const [mode, setModeState] = useState<Mode>(() => read("gomon-mode", "system") as Mode)
  const systemDark = useSystemDark()
  const resolvedDark = mode === "dark" || (mode === "system" && systemDark)

  const setThemeId = useCallback((id: string) => {
    setThemeIdState(id)
    write("gomon-theme", id)
  }, [])
  const setMode = useCallback((m: Mode) => {
    setModeState(m)
    write("gomon-mode", m)
  }, [])

  useEffect(() => {
    const root = document.documentElement
    root.classList.toggle("dark", resolvedDark)
    const theme = themes.find((t) => t.id === themeId) ?? themes[0]
    if (!theme) return
    // Remove variables from a previous theme, then apply this one.
    const prev = (root.dataset.themeVars ?? "").split(",").filter(Boolean)
    prev.forEach((k) => root.style.removeProperty(k))
    const vars = resolvedDark ? theme.dark : theme.light
    Object.entries(vars).forEach(([k, v]) => root.style.setProperty(k, v))
    root.dataset.themeVars = Object.keys(vars).join(",")
    write("gomon-theme-cache", JSON.stringify({ light: theme.light, dark: theme.dark }))
  }, [themes, themeId, resolvedDark])

  const value = useMemo(() => ({ themes, themeId, setThemeId, mode, setMode, resolvedDark }), [themes, themeId, setThemeId, mode, setMode, resolvedDark])
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>
}

export function useTheme() {
  const c = useContext(Ctx)
  if (!c) throw new Error("useTheme outside ThemeProvider")
  return c
}
