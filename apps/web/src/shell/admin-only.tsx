import type { ReactNode } from 'react'
import { useAuthStore } from '@/store/auth'
import { Empty } from '@/ui/controls'
import { PageHead } from '@/ui/page'

// Hiding an admin page from the sidebar is presentation, not access control:
// a bookmark or a shared link still lands on it. The API refuses every call,
// so nothing leaks, but a page of buttons that fail on click is a poor way to
// find out. This says why up front.
export function AdminOnly({ title, children }: { title: string; children: ReactNode }) {
  const admin = useAuthStore((s) => s.user?.role === 'admin')
  if (admin) return <>{children}</>
  return (
    <div className="scroll">
      <div className="page narrow">
        <PageHead title={title} />
        <Empty title="Only administrators can open this.">Ask an administrator if you need it.</Empty>
      </div>
    </div>
  )
}
