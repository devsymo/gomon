import { Navigate, Route, Routes } from "react-router-dom"
import { useQuery } from "@tanstack/react-query"
import { Loader2 } from "lucide-react"
import { api, type User } from "@/lib/api"
import { AppLayout } from "@/components/layout/AppLayout"
import Login from "@/pages/Login"
import Dashboard from "@/pages/Dashboard"
import Hosts from "@/pages/Hosts"
import HostDetail from "@/pages/HostDetail"
import StatusDetail from "@/pages/StatusDetail"
import Events from "@/pages/Events"
import SNMP from "@/pages/SNMP"
import Alerts from "@/pages/Alerts"
import Settings from "@/pages/Settings"

function RequireAuth({ children }: { children: React.ReactNode }) {
  const { data, isLoading, isError } = useQuery({ queryKey: ["me"], queryFn: () => api.get<User>("/auth/me"), retry: false, refetchInterval: false })
  if (isLoading) {
    return (
      <div className="flex h-full items-center justify-center">
        <Loader2 className="text-muted-foreground size-6 animate-spin" />
      </div>
    )
  }
  if (isError || !data) return <Navigate to="/login" replace />
  return <>{children}</>
}

export default function App() {
  return (
    <Routes>
      <Route path="/login" element={<Login />} />
      <Route
        element={
          <RequireAuth>
            <AppLayout />
          </RequireAuth>
        }
      >
        <Route index element={<Dashboard />} />
        <Route path="hosts" element={<Hosts />} />
        <Route path="hosts/:host" element={<HostDetail />} />
        <Route path="hosts/:host/:test" element={<StatusDetail />} />
        <Route path="events" element={<Events />} />
        <Route path="snmp" element={<SNMP />} />
        <Route path="alerts" element={<Alerts />} />
        <Route path="settings" element={<Settings />} />
        <Route path="settings/:tab" element={<Settings />} />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Route>
    </Routes>
  )
}
