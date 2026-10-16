import { Motd, ServerIcon } from '@/ui/pixel'

/** The entry players see in Multiplayer: icon, name, MOTD, player count, signal. */
export function MultiplayerEntry({ id, icon, name, motd, up, online, max }: { id: string; icon?: string | null; name: string; motd: string; up: boolean; online: number; max?: number }) {
  return (
    <div className="mc-entry" role="img" aria-label={`${name} as it appears in the multiplayer list`}>
      <ServerIcon id={id} icon={icon} size={64} className="mc-icon" />
      <div className="grow">
        <div className="mc-name">{name}</div>
        <div className="mc-motd">{up ? <Motd text={motd} /> : <span style={{ color: '#AA0000' }}>Can&apos;t connect to server</span>}</div>
      </div>
      <div className="mc-count">
        {up && (
          <span>
            <span style={{ color: '#AAAAAA' }}>{online}</span>
            <span style={{ color: '#555555' }}>/</span>
            <span style={{ color: '#AAAAAA' }}>{max ?? '?'}</span>
          </span>
        )}
        <span className="mc-bars" aria-hidden="true">
          {[3, 5, 7, 9, 10].map((h, i) => (
            <i key={i} style={{ height: h, background: up ? '#55ff55' : '#555' }} />
          ))}
        </span>
      </div>
    </div>
  )
}
