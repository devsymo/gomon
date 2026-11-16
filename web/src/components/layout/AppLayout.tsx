import { useEffect, useState } from "react"
import { Link, NavLink, Outlet, useLocation, useNavigate } from "react-router-dom"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import {
  Activity, Bell, History, LayoutDashboard, LayoutGrid, TriangleAlert, LogOut, Menu, Monitor, Moon, Network, Palette, Server, Settings, Sun, X,
} from "lucide-react"
import { api, type Overview, type User } from "@/lib/api"
import { useTheme, type Mode } from "@/lib/theme"
import { useLiveEvents } from "@/hooks/useLive"
import { cn } from "@/lib/utils"
import { Button } from "@/components/ui/button"
import { Separator } from "@/components/ui/separator"
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuLabel, DropdownMenuRadioGroup, DropdownMenuRadioItem, DropdownMenuSeparator, DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { Logo } from "@/components/Logo"

const nav = [
  { to: "/", label: "Dashboard", icon: LayoutDashboard, end: true },
  { to: "/nongreen", label: "Non-green", icon: TriangleAlert },
  { to: "/pages", label: "Pages", icon: LayoutGrid },
  { to: "/hosts", label: "Hosts", icon: Server },
  { to: "/events", label: "Events", icon: History },
  { to: "/snmp", label: "SNMP", icon: Network },
  { to: "/alerts", label: "Alerts", icon: Bell },
  { to: "/settings", label: "Settings", icon: Settings },
]

function usePersisted(key: string, def: boolean): [boolean, (v: boolean) => void] {
  const [v, setV] = useState(() => {
    try {
      const s = localStorage.getItem(key)
      return s === null ? def : s === "true"
    } catch {
      return def
    }
  })
  return [v, (nv: boolean) => {
    setV(nv)
    try {
      localStorage.setItem(key, String(nv))
    } catch {
      /* ignore */
    }
  }]
}

function Sidebar({ onNavigate }: { onNavigate?: () => void }) {
  const qc = useQueryClient()
  const navigate = useNavigate()
  const { data: me } = useQuery({ queryKey: ["me"], queryFn: () => api.get<User>("/auth/me") })
  const { data: ov } = useQuery({ queryKey: ["overview"], queryFn: () => api.get<Overview>("/overview"), refetchInterval: 60_000 })
  const { data: sys } = useQuery({ queryKey: ["system"], queryFn: () => api.get<{ version: string }>("/system"), staleTime: 300_000 })
  const logout = useMutation({
    mutationFn: () => api.post("/auth/logout"),
    onSettled: () => {
      qc.clear()
      navigate("/login")
    },
  })
  const problems = (ov?.statusCounts.red ?? 0) + (ov?.statusCounts.yellow ?? 0)
  return (
    <div className="bg-sidebar border-sidebar-border flex h-full w-64 flex-col border-r">
      <div className="p-6">
        <Link to="/" onClick={onNavigate} className="text-sidebar-foreground flex items-center gap-2 text-lg font-semibold hover:opacity-80">
          <Logo className="size-5" />
          gomon
        </Link>
      </div>
      <nav className="flex min-h-0 flex-1 flex-col overflow-y-auto px-3">
        <div className="space-y-1">
          {nav.map((n) => (
            <NavLink
              key={n.to}
              to={n.to}
              end={n.end}
              onClick={onNavigate}
              className={({ isActive }) =>
                cn(
                  "flex items-center gap-3 rounded-md px-3 py-2 text-sm font-medium transition-all duration-200",
                  isActive
                    ? "bg-sidebar-primary text-sidebar-primary-foreground"
                    : "text-sidebar-foreground hover:bg-sidebar-accent hover:text-sidebar-accent-foreground"
                )
              }
            >
              <n.icon className="size-4" />
              {n.label}
              {n.to === "/" && problems > 0 && (
                <span className="bg-status-red ml-auto rounded-full px-1.5 text-[10px] font-semibold text-white">{problems}</span>
              )}
            </NavLink>
          ))}
        </div>
        <div className="mt-auto pt-3">
          <div className="text-sidebar-foreground/60 px-3 pb-2 text-xs">
            Signed in as <span className="text-sidebar-foreground font-medium">{me?.username}</span>
          </div>
          <Button variant="ghost" className="text-sidebar-foreground w-full justify-start" onClick={() => logout.mutate()}>
            <LogOut className="mr-1 size-4" />
            Logout
          </Button>
        </div>
      </nav>
      <div className="shrink-0 p-3">
        <Separator className="mb-3" />
        <div className="text-sidebar-foreground/40 flex items-center justify-between px-3 pb-2 text-[10px] select-none">
          <span className="text-sidebar-foreground/50 font-medium">Version {sys?.version ?? "…"}</span>
          <span>Xymon compatible</span>
        </div>
      </div>
    </div>
  )
}

function ThemeMenu() {
  const { themes, themeId, setThemeId, mode, setMode } = useTheme()
  const Icon = mode === "dark" ? Moon : mode === "light" ? Sun : Monitor
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" size="icon" aria-label="Theme">
          <Icon className="size-5" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-52">
        <DropdownMenuLabel>Mode</DropdownMenuLabel>
        <DropdownMenuRadioGroup value={mode} onValueChange={(v) => setMode(v as Mode)}>
          <DropdownMenuRadioItem value="light"><Sun className="size-4" /> Light</DropdownMenuRadioItem>
          <DropdownMenuRadioItem value="dark"><Moon className="size-4" /> Dark</DropdownMenuRadioItem>
          <DropdownMenuRadioItem value="system"><Monitor className="size-4" /> System</DropdownMenuRadioItem>
        </DropdownMenuRadioGroup>
        <DropdownMenuSeparator />
        <DropdownMenuLabel className="flex items-center gap-1.5"><Palette className="size-3.5" /> Theme</DropdownMenuLabel>
        <DropdownMenuRadioGroup value={themeId} onValueChange={setThemeId}>
          {themes.map((t) => (
            <DropdownMenuRadioItem key={t.id} value={t.id}>
              <span className="size-3 rounded-full border" style={{ background: t.light["--primary"] }} />
              {t.name}
            </DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

const titles: [RegExp, string][] = [
  [/^\/$/, "Dashboard"],
  [/^\/hosts\/[^/]+\/[^/]+/, "Status"],
  [/^\/hosts\/[^/]+/, "Host"],
  [/^\/hosts/, "Hosts"],
  [/^\/events/, "Events"],
  [/^\/nongreen/, "Non-green"],
  [/^\/pages/, "Pages"],
  [/^\/snmp/, "SNMP"],
  [/^\/alerts/, "Alerts"],
  [/^\/settings/, "Settings"],
]

export function AppLayout() {
  const [collapsed, setCollapsed] = usePersisted("gomon-sidebar-collapsed", false)
  const [mobileOpen, setMobileOpen] = useState(false)
  const loc = useLocation()
  const navigate = useNavigate()
  const live = useLiveEvents()
  const { data: ov } = useQuery({ queryKey: ["overview"], queryFn: () => api.get<Overview>("/overview") })
  const title = titles.find(([re]) => re.test(loc.pathname))?.[1] ?? "gomon"

  useEffect(() => {
    const onUnauth = () => navigate("/login")
    window.addEventListener("gomon:unauthorized", onUnauth)
    return () => window.removeEventListener("gomon:unauthorized", onUnauth)
  }, [navigate])

  useEffect(() => {
    const bad = (ov?.statusCounts.red ?? 0)
    document.title = bad > 0 ? `(${bad}) ${title} · gomon` : `${title} · gomon`
  }, [title, ov])

  return (
    <div className="bg-background flex h-[100dvh]">
      <div className={cn("hidden overflow-hidden transition-all duration-300 ease-out lg:flex", collapsed ? "w-0 opacity-0" : "w-64 opacity-100")}>
        <div className="w-64 shrink-0">
          <Sidebar />
        </div>
      </div>
      {mobileOpen && (
        <div className="fixed inset-0 z-40 lg:hidden">
          <div className="absolute inset-0 bg-black/50" onClick={() => setMobileOpen(false)} />
          <div className="relative h-full w-64">
            <Sidebar onNavigate={() => setMobileOpen(false)} />
            <Button variant="ghost" size="icon" className="absolute top-4 right-2" onClick={() => setMobileOpen(false)}>
              <X className="size-4" />
            </Button>
          </div>
        </div>
      )}
      <div className="relative flex min-w-0 flex-1 flex-col">
        <header className="flex h-14 shrink-0 items-center gap-2 border-b px-4 lg:px-6">
          <Tooltip>
            <TooltipTrigger asChild>
              <Button variant="ghost" size="icon" className="hidden lg:flex" onClick={() => setCollapsed(!collapsed)}>
                <Menu className={cn("size-5 transition-transform duration-300", collapsed && "rotate-90")} />
              </Button>
            </TooltipTrigger>
            <TooltipContent side="bottom">{collapsed ? "Show sidebar" : "Hide sidebar"}</TooltipContent>
          </Tooltip>
          <Button variant="ghost" size="icon" className="lg:hidden" onClick={() => setMobileOpen(true)}>
            <Menu className="size-5" />
          </Button>
          {collapsed && (
            <Link to="/" className="hidden items-center gap-2 font-semibold lg:flex">
              <Logo className="size-5" /> gomon
            </Link>
          )}
          <h1 className="text-lg font-semibold">{title}</h1>
          <div className="ml-auto flex items-center gap-1">
            <Tooltip>
              <TooltipTrigger asChild>
                <span className="text-muted-foreground mr-2 hidden items-center gap-1.5 text-xs sm:flex">
                  <Activity className={cn("size-3.5", live ? "text-status-green" : "text-muted-foreground")} />
                  {live ? "Live" : "Offline"}
                </span>
              </TooltipTrigger>
              <TooltipContent side="bottom">{live ? "Receiving live status updates" : "Live updates disconnected, retrying"}</TooltipContent>
            </Tooltip>
            <ThemeMenu />
          </div>
        </header>
        <main className="flex-1 overflow-y-auto">
          <Outlet />
        </main>
      </div>
    </div>
  )
}
