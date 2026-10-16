import { useNavigate } from '@tanstack/react-router'
import { Btn } from '@/ui/controls'
import { toast } from '@/ui/toasts'
import { copyText } from '@/ui/util'

/**
 * A page that threw while rendering. Only the page is replaced: the sidebar,
 * search and everything else keep working, and the details are there to copy
 * for whoever looks into it. The router's error component, so a crash in any
 * route lands here.
 */
export function CrashPage({ error, reset, info }: { error: Error; reset?: () => void; info?: { componentStack?: string } }) {
  const navigate = useNavigate()
  const stack = (info?.componentStack || error.stack || '').trim().split('\n').slice(0, 4).join('\n')
  const details = `${error.name}: ${error.message}\nPage: ${window.location.pathname}${window.location.search}\nPanel ${__APP_VERSION__}\n${stack}`
  const again = () => (reset ? reset() : window.location.reload())
  return (
    <div className="scroll">
      <div className="page narrow">
        <div className="load-error" role="alert" style={{ marginTop: 24 }}>
          <div className="empty-title">Something went wrong on this page.</div>
          <p className="t2">The rest of the panel still works. Loading the page again usually fixes it. If it keeps happening, the details below help whoever looks into it.</p>
          <div className="row" style={{ gap: 8, marginTop: 14 }}>
            <Btn variant="primary" onClick={again}>
              Load it again
            </Btn>
            <Btn
              onClick={() => {
                reset?.()
                void navigate({ to: '/' })
              }}
            >
              Go home
            </Btn>
            <Btn
              variant="quiet"
              onClick={() => {
                void copyText(details).then((ok) => toast(ok ? 'ok' : 'bad', ok ? 'Copied the details' : 'Couldn’t copy the details'))
              }}
            >
              Copy details
            </Btn>
          </div>
          <details style={{ marginTop: 16 }}>
            <summary className="small t3" style={{ cursor: 'pointer' }}>
              Details
            </summary>
            <div className="codeblock" style={{ marginTop: 8, paddingRight: 15 }}>
              {details}
            </div>
          </details>
        </div>
      </div>
    </div>
  )
}
