interface HeaderProps {
  title: string
  description?: string
  actions?: React.ReactNode
}

// The title bar of a screen not yet moved to the new design. On phones the
// shell's own bar opens the menu.
export function Header({ title, description, actions }: HeaderProps) {
  return (
    <div className="flex items-center justify-between gap-3 px-4 sm:px-6 py-4 border-b border-border bg-surface/50">
      <div className="min-w-0">
        <h1 className="truncate text-lg font-semibold text-text-primary">{title}</h1>
        {description && <p className="truncate text-sm text-text-secondary mt-0.5">{description}</p>}
      </div>
      {actions && <div className="flex flex-shrink-0 items-center gap-2 sm:gap-3">{actions}</div>}
    </div>
  )
}
