import { clsx } from 'clsx'

export function Card({ className, children, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      className={clsx(
        // Flat dark surfaces read as one plane; a hairline top highlight plus a
        // soft ambient shadow lifts cards off the background without looking
        // skeuomorphic. The inset white edge fakes light coming from above.
        'rounded-lg border border-border bg-surface shadow-[0_1px_2px_0_rgb(0_0_0/0.3),inset_0_1px_0_0_rgb(255_255_255/0.03)]',
        className,
      )}
      {...props}
    >
      {children}
    </div>
  )
}

export function CardHeader({ className, children, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  return (
    <div className={clsx('flex flex-col space-y-1.5 p-5 border-b border-border', className)} {...props}>
      {children}
    </div>
  )
}

export function CardTitle({ className, children, ...props }: React.HTMLAttributes<HTMLHeadingElement>) {
  return (
    <h3 className={clsx('text-base font-semibold text-text-primary', className)} {...props}>
      {children}
    </h3>
  )
}

export function CardContent({ className, children, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  return (
    <div className={clsx('p-5', className)} {...props}>
      {children}
    </div>
  )
}

export function CardFooter({ className, children, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  return (
    <div className={clsx('flex items-center p-5 pt-0', className)} {...props}>
      {children}
    </div>
  )
}
