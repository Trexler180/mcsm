import { createRoute, useNavigate } from '@tanstack/react-router'
import { useState } from 'react'
import { Server } from 'lucide-react'
import { Route as rootRoute } from './__root'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { useAuthStore } from '@/store/auth'

function LoginPage() {
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  const [mfaRequired, setMfaRequired] = useState(false)
  const [useRecovery, setUseRecovery] = useState(false)
  const [code, setCode] = useState('')
  const { login } = useAuthStore()
  const navigate = useNavigate()

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    setError('')
    setLoading(true)
    try {
      const opts = mfaRequired
        ? useRecovery
          ? { recoveryCode: code }
          : { totpCode: code }
        : undefined
      const res = await login(email, password, opts)
      if (res.mfaRequired) {
        // Password accepted; prompt for the second factor.
        setMfaRequired(true)
        setLoading(false)
        return
      }
      navigate({ to: '/' })
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Login failed')
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="min-h-screen bg-background flex items-center justify-center p-4">
      <div className="w-full max-w-sm">
        <div className="flex flex-col items-center mb-8">
          <div className="w-12 h-12 rounded-xl bg-accent flex items-center justify-center mb-4">
            <Server className="h-6 w-6 text-black" />
          </div>
          <h1 className="text-2xl font-bold text-text-primary">MCSM</h1>
          <p className="text-sm text-text-secondary mt-1">
            Mod-aware operations for Minecraft servers
          </p>
        </div>

        <form onSubmit={handleSubmit} className="space-y-4">
          <div className="space-y-1.5">
            <Label htmlFor="email">Email</Label>
            <Input
              id="email"
              type="email"
              placeholder="admin@example.com"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              required
              autoFocus
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="password">Password</Label>
            <Input
              id="password"
              type="password"
              placeholder="••••••••"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              required
              disabled={mfaRequired}
            />
          </div>

          {mfaRequired && (
            <div className="space-y-1.5">
              <Label htmlFor="code">
                {useRecovery ? 'Recovery code' : 'Authentication code'}
              </Label>
              <Input
                id="code"
                inputMode={useRecovery ? 'text' : 'numeric'}
                autoComplete="one-time-code"
                placeholder={useRecovery ? 'xxxx-xxxx-xxxx-xxxx' : '123456'}
                value={code}
                onChange={(e) => setCode(e.target.value)}
                required
                autoFocus
              />
              <button
                type="button"
                className="text-xs text-text-secondary hover:text-text-primary underline"
                onClick={() => {
                  setUseRecovery((v) => !v)
                  setCode('')
                }}
              >
                {useRecovery
                  ? 'Use an authenticator code instead'
                  : "Can't access your authenticator? Use a recovery code"}
              </button>
            </div>
          )}

          {error && (
            <p className="text-sm text-red-400 bg-red-900/20 border border-red-800/50 rounded-md px-3 py-2">
              {error}
            </p>
          )}

          <Button type="submit" className="w-full" loading={loading}>
            {mfaRequired ? 'Verify' : 'Sign in'}
          </Button>
        </form>
      </div>
    </div>
  )
}

export const Route = createRoute({
  getParentRoute: () => rootRoute,
  path: '/login',
  component: LoginPage,
})
