import { Menu } from 'lucide-react'
import { useUiStore } from '@/store/ui'

interface HeaderProps {
  title: string
  description?: string
  actions?: React.ReactNode
}

export function Header({ title, description, actions }: HeaderProps) {
  const toggleSidebar = useUiStore((s) => s.toggleSidebar)

  return (
    <div className="flex items-center justify-between gap-3 px-4 sm:px-6 py-4 border-b border-border bg-surface/50">
      <div className="flex min-w-0 items-center gap-3">
        <button
          onClick={toggleSidebar}
          aria-label="Open menu"
          className="-ml-1 rounded-md p-2 text-text-secondary hover:bg-surface-2 hover:text-text-primary md:hidden"
        >
          <Menu className="h-5 w-5" />
        </button>
        <div className="min-w-0">
          <h1 className="truncate text-lg font-semibold text-text-primary">{title}</h1>
          {description && (
            <p className="truncate text-sm text-text-secondary mt-0.5">{description}</p>
          )}
        </div>
      </div>
      {actions && <div className="flex flex-shrink-0 items-center gap-2 sm:gap-3">{actions}</div>}
    </div>
  )
}
