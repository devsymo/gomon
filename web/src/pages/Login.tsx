import { useState } from "react"
import { useNavigate, useSearchParams } from "react-router-dom"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { KeyRound, Loader2 } from "lucide-react"
import { api, type AuthInfo, type User } from "@/lib/api"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Separator } from "@/components/ui/separator"
import { Logo } from "@/components/Logo"

export default function Login() {
  const [params] = useSearchParams()
  const navigate = useNavigate()
  const qc = useQueryClient()
  const { data: info, isLoading } = useQuery({ queryKey: ["authinfo"], queryFn: () => api.get<AuthInfo>("/auth/info"), refetchInterval: false })
  const [username, setUsername] = useState("")
  const [password, setPassword] = useState("")
  const [confirm, setConfirm] = useState("")
  const [error, setError] = useState(params.get("error") ?? "")
  const setup = info?.setupRequired

  const submit = useMutation({
    mutationFn: () => {
      if (setup && password !== confirm) throw new Error("Passwords do not match")
      return api.post<User>(setup ? "/auth/setup" : "/auth/login", { username, password })
    },
    onSuccess: (u) => {
      qc.setQueryData(["me"], u)
      qc.invalidateQueries({ queryKey: ["authinfo"] })
      navigate("/")
    },
    onError: (e: Error) => setError(e.message),
  })

  return (
    <div className="bg-background flex min-h-full items-center justify-center p-4">
      <Card className="w-full max-w-sm">
        <CardHeader className="flex-col items-center text-center">
          <Logo className="mb-2 size-10" />
          <CardTitle className="text-2xl">{setup ? "Welcome to gomon" : "gomon"}</CardTitle>
          <CardDescription>{setup ? "Create the administrator account to get started" : "Sign in to your monitoring dashboard"}</CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          {isLoading ? (
            <div className="flex justify-center py-6"><Loader2 className="size-5 animate-spin" /></div>
          ) : (
            <>
              {info?.builtinLogin && (
                <form
                  className="space-y-4"
                  onSubmit={(e) => {
                    e.preventDefault()
                    setError("")
                    submit.mutate()
                  }}
                >
                  <div className="grid gap-2">
                    <Label htmlFor="u">Username</Label>
                    <Input id="u" autoComplete="username" value={username} onChange={(e) => setUsername(e.target.value)} required autoFocus />
                  </div>
                  <div className="grid gap-2">
                    <Label htmlFor="p">Password</Label>
                    <Input id="p" type="password" autoComplete={setup ? "new-password" : "current-password"} value={password} onChange={(e) => setPassword(e.target.value)} required minLength={setup ? 8 : undefined} />
                  </div>
                  {setup && (
                    <div className="grid gap-2">
                      <Label htmlFor="c">Confirm password</Label>
                      <Input id="c" type="password" autoComplete="new-password" value={confirm} onChange={(e) => setConfirm(e.target.value)} required />
                    </div>
                  )}
                  <Button type="submit" className="w-full" disabled={submit.isPending}>
                    {submit.isPending && <Loader2 className="animate-spin" />}
                    {setup ? "Create account" : "Sign in"}
                  </Button>
                </form>
              )}
              {info?.oidcEnabled && !setup && (
                <>
                  {info.builtinLogin && (
                    <div className="flex items-center gap-3">
                      <Separator className="flex-1" />
                      <span className="text-muted-foreground text-xs">or</span>
                      <Separator className="flex-1" />
                    </div>
                  )}
                  <Button variant="outline" className="w-full" asChild>
                    <a href="/api/auth/oidc/login">
                      <KeyRound /> {info.oidcLabel || "Sign in with SSO"}
                    </a>
                  </Button>
                </>
              )}
              {error && <p className="text-destructive text-center text-sm">{error}</p>}
            </>
          )}
        </CardContent>
      </Card>
    </div>
  )
}
