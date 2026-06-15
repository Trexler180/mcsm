import { forwardRef } from 'react'
import { clsx } from 'clsx'

type Variant = 'default' | 'destructive' | 'outline' | 'ghost' | 'link'
type Size = 'sm' | 'md' | 'lg' | 'icon'

interface ButtonProps extends React.ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: Variant
  size?: Size
  loading?: boolean
}

const variantClasses: Record<Variant, string> = {
  default: 'bg-accent text-black hover:bg-accent-hover font-medium',
  destructive: 'bg-red-600 text-white hover:bg-red-700',
  outline: 'border border-border text-text-primary hover:bg-surface-2',
  ghost: 'text-text-secondary hover:text-text-primary hover:bg-surface-2',
  link: 'text-accent underline-offset-4 hover:underline p-0 h-auto',
}

const sizeClasses: Record<Size, string> = {
  // touch:h-9 — 28px is below a usable tap target on touch screens.
  sm: 'h-7 touch:h-9 px-3 text-xs rounded',
  md: 'h-9 px-4 text-sm rounded-md',
  lg: 'h-11 px-6 text-base rounded-md',
  icon: 'h-9 w-9 rounded-md flex items-center justify-center',
}

export const Button = forwardRef<HTMLButtonElement, ButtonProps>(
  ({ className, variant = 'default', size = 'md', loading, disabled, children, ...props }, ref) => (
    <button
      ref={ref}
      disabled={disabled || loading}
      className={clsx(
        'inline-flex items-center justify-center gap-2 transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent disabled:opacity-50 disabled:cursor-not-allowed',
        variantClasses[variant],
        sizeClasses[size],
        className,
      )}
      {...props}
    >
      {loading && (
        <svg className="animate-spin h-4 w-4" viewBox="0 0 24 24" fill="none">
          <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
          <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
        </svg>
      )}
      {children}
    </button>
  ),
)
Button.displayName = 'Button'
