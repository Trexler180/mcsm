import { useState, type CSSProperties } from 'react'
import { useQuery } from '@tanstack/react-query'
import { api } from '@/lib/api'
import { hashStr } from '@/ui/util'

/** A mod's icon: its project's, or a tile with its initial when there isn't
 *  one (an uploaded jar, an unreachable source). */
export function ModIcon({ url, name, size = 32, style }: { url?: string | null; name: string; size?: number; style?: CSSProperties }) {
  const [failed, setFailed] = useState(false)
  if (url && !failed) {
    return <img className="mod-icon" src={url} alt="" width={size} height={size} loading="lazy" style={{ borderRadius: Math.round(size / 5), objectFit: 'cover', flex: 'none', ...style }} onError={() => setFailed(true)} />
  }
  const hue = hashStr(name) % 360
  return (
    <span
      className="mod-icon"
      aria-hidden="true"
      style={{
        width: size,
        height: size,
        borderRadius: Math.round(size / 5),
        display: 'grid',
        placeItems: 'center',
        flex: 'none',
        fontSize: size * 0.42,
        fontWeight: 600,
        color: '#fff',
        background: `hsl(${hue} 28% 38%)`,
        ...style,
      }}
    >
      {modInitial(name)}
    </span>
  )
}

/** The letter a mod's tile shows: its first letter or digit, so a name like
 *  "[1.21] Lithium" doesn't come out as "[". */
export function modInitial(name: string): string {
  return (name.match(/[\p{L}\p{N}]/u)?.[0] ?? '?').toUpperCase()
}

/** An installed mod's icon, looked up from its project once and cached. */
export function InstalledModIcon({ serverId, source, sourceId, name, size, style }: { serverId: string; source: string; sourceId: string | null; name: string; size?: number; style?: CSSProperties }) {
  const lookup = !!sourceId && (source === 'modrinth' || source === 'curseforge' || source === 'hangar')
  const project = useQuery({
    queryKey: ['mod-project', source, sourceId],
    queryFn: () => api.mods.getProject(serverId, sourceId as string, source),
    enabled: lookup,
    staleTime: 24 * 60 * 60_000,
    retry: false,
  }).data
  return <ModIcon url={project?.icon_url} name={name} size={size} style={style} />
}
