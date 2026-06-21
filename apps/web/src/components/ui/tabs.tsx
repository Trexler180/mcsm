import { clsx } from 'clsx'
import { createContext, useContext } from 'react'

const TabsContext = createContext<{ value: string; onChange: (v: string) => void }>({
  value: '',
  onChange: () => {},
})

interface TabsProps {
  value: string
  onValueChange: (value: string) => void
  children: React.ReactNode
  className?: string
}

export function Tabs({ value, onValueChange, children, className }: TabsProps) {
  return (
    <TabsContext.Provider value={{ value, onChange: onValueChange }}>
      <div className={className}>{children}</div>
    </TabsContext.Provider>
  )
}

export function TabsList({ className, children, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      className={clsx(
        // Scroll horizontally rather than overflow when triggers don't fit on
        // narrow screens; hide the scrollbar to keep the underline clean.
        'flex h-9 items-center gap-1 border-b border-border w-full overflow-x-auto scrollbar-none',
        className,
      )}
      {...props}
    >
      {children}
    </div>
  )
}

interface TabsTriggerProps {
  value: string
  children: React.ReactNode
  className?: string
}

export function TabsTrigger({ value, children, className }: TabsTriggerProps) {
  const ctx = useContext(TabsContext)
  const active = ctx.value === value
  return (
    <button
      onClick={() => ctx.onChange(value)}
      className={clsx(
        'flex-shrink-0 whitespace-nowrap px-4 py-2 text-sm font-medium transition-colors focus:outline-none',
        'border-b-2 -mb-px',
        active
          ? 'text-text-primary border-accent'
          : 'text-text-secondary border-transparent hover:text-text-primary hover:border-border-hover',
        className,
      )}
    >
      {children}
    </button>
  )
}

interface TabsContentProps {
  value: string
  children: React.ReactNode
  className?: string
}

export function TabsContent({ value, children, className }: TabsContentProps) {
  const ctx = useContext(TabsContext)
  if (ctx.value !== value) return null
  return <div className={clsx(className ?? 'pt-4')}>{children}</div>
}
