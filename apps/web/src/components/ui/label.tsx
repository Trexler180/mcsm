import { clsx } from 'clsx'

interface LabelProps extends React.LabelHTMLAttributes<HTMLLabelElement> {}

export function Label({ className, children, ...props }: LabelProps) {
  return (
    <label
      className={clsx('text-sm font-medium text-text-secondary leading-none', className)}
      {...props}
    >
      {children}
    </label>
  )
}
