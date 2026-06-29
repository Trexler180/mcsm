import { createRoute } from '@tanstack/react-router'
import { Route as rootRoute } from './__root'
import { Header } from '@/components/layout/header'
import { SecuritySection } from '@/components/settings/security'

function AccountPage() {
  return (
    <div>
      <Header
        title="Account"
        description="Your sign-in security and active sessions"
      />
      <div className="p-4 sm:p-6 max-w-2xl">
        <SecuritySection />
      </div>
    </div>
  )
}

export const Route = createRoute({
  getParentRoute: () => rootRoute,
  path: '/account',
  component: AccountPage,
})
