import { createRoute, useNavigate, useParams } from '@tanstack/react-router'
import { Route as rootRoute } from './__root'
import { AccountPage, accountSectionOf } from '@/pages/account'

function AccountView() {
  const { section } = useParams({ strict: false }) as { section?: string }
  const navigate = useNavigate()
  return <AccountPage section={accountSectionOf(section)} setSection={(s) => void navigate({ to: s === 'you' ? '/account' : (`/account/${s}` as '/account') })} />
}

export const Route = createRoute({
  getParentRoute: () => rootRoute,
  path: '/account',
  component: AccountView,
})

export const AccountSectionRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/account/$section',
  component: AccountView,
})
