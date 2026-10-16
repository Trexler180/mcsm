import type { ReactNode } from 'react'

/** A page's title, an optional line under it, and its actions on the right. */
export function PageHead({ title, lede, children }: { title: ReactNode; lede?: ReactNode; children?: ReactNode }) {
  return (
    <div className="page-head">
      <div className="grow">
        <h1 className="page-title">{title}</h1>
        {lede && <p className="page-lede">{lede}</p>}
      </div>
      {children}
    </div>
  )
}
