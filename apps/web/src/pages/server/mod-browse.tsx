import { useEffect, useMemo, useRef, useState, type ComponentProps } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import ReactMarkdown from 'react-markdown'
import rehypeRaw from 'rehype-raw'
import rehypeSanitize from 'rehype-sanitize'
import remarkGfm from 'remark-gfm'
import { api } from '@/lib/api'
import type { InstalledMod, ModProjectType, ModSearchHit, ModSortIndex, ModSource, Server } from '@/lib/types'
import { Btn, Check, Empty, Field, Notice, Search, Select, Tabs, TextBtn, type StatusKey } from '@/ui/controls'
import { act } from '@/ui/failure'
import { fmtCompact, fmtDate, fmtNum, relTime } from '@/ui/format'
import { Dialog, Sheet } from '@/ui/overlays'
import { useCan } from '@/ui/perm'
import { PLATFORMS, platformLabel } from '@/ui/platforms'
import { Loaded, SheetSkeleton, SkItems } from '@/ui/skeleton'
import { toast } from '@/ui/toasts'
import { cx, toMs } from '@/ui/util'
import { LOADERS, SIDES, SOURCE_LABEL, contradicts, fitsServer, gameVersions, pageWindow, sideNote, topicsOf, type Side } from './mod-shared'
import { ModIcon } from './mod-icon'

// Finding more: search one source, narrow by version, loader, where it runs
// and category, and install in one click, or look closer first.

const PAGE = 20

// Each source ranks differently: CurseForge has no relevance or follows of its
// own (both mean popularity), Hangar's follows are stars, SpigotMC's likes.
function sortsFor(source: ModSource): { value: ModSortIndex; label: string }[] {
  const follows = source === 'hangar' ? 'Most starred' : source === 'spigotmc' ? 'Most liked' : 'Most followed'
  const all: { value: ModSortIndex; label: string }[] = [
    { value: 'relevance', label: source === 'curseforge' || source === 'spigotmc' ? 'Most popular' : 'Best match' },
    { value: 'downloads', label: 'Most downloaded' },
    { value: 'follows', label: follows },
    { value: 'updated', label: 'Recently updated' },
    { value: 'newest', label: 'Newest' },
  ]
  return source === 'curseforge' ? all.filter((s) => s.value !== 'follows') : all
}

// Focusing a field on a phone opens its keyboard over the results.
const finePointer = () => typeof window !== 'undefined' && !!window.matchMedia?.('(pointer: fine)').matches

export function kindsFor(platform: string): { value: ModProjectType; label: string }[] {
  const c = PLATFORMS[platform]?.content
  if (c === 'plugin') return [
    { value: 'plugin', label: 'Plugins' },
    { value: 'datapack', label: 'Datapacks' },
  ]
  if (c === 'datapack') return [{ value: 'datapack', label: 'Datapacks' }]
  return [
    { value: 'mod', label: 'Mods' },
    { value: 'modpack', label: 'Modpacks' },
    { value: 'datapack', label: 'Datapacks' },
  ]
}

/** Where each kind can be found. Modpacks install from Modrinth's format only. */
export function sourcesFor(kind: ModProjectType): ModSource[] {
  if (kind === 'plugin') return ['hangar', 'modrinth', 'spigotmc']
  if (kind === 'modpack') return ['modrinth']
  return ['modrinth', 'curseforge']
}

export function Browse({ server, status, installed, onTotal }: { server: Server; status: StatusKey; installed: InstalledMod[]; onTotal: (n: number | null) => void }) {
  const qc = useQueryClient()
  const sources = useQuery({ queryKey: ['mod-sources', server.id], queryFn: () => api.mods.sources(server.id), staleTime: 60 * 60_000, retry: false }).data
  const kinds = kindsFor(server.platform)
  const [kind, setKind] = useState<ModProjectType>(kinds[0].value)
  const available = sourcesFor(kind).filter((s) => !sources || sources[s] !== false)
  const [source, setSource] = useState<ModSource>(available[0] ?? 'modrinth')
  useEffect(() => {
    if (!available.includes(source) && available[0]) setSource(available[0])
  }, [available, source])
  const [q, setQ] = useState('')
  const [dq, setDq] = useState('')
  useEffect(() => {
    const t = setTimeout(() => setDq(q.trim()), 300)
    return () => clearTimeout(t)
  }, [q])
  const [cats, setCats] = useState<string[]>([])
  const [sort, setSort] = useState<ModSortIndex>('relevance')
  const [mc, setMc] = useState(server.mc_version)
  const [loader, setLoader] = useState(PLATFORMS[server.platform]?.content === 'mod' ? server.platform : '')
  const [side, setSide] = useState<Side>('')
  const [hideMine, setHideMine] = useState(false)
  const [page, setPage] = useState(0)
  useEffect(() => setPage(0), [dq, cats, sort, mc, loader, side, source, kind])
  useEffect(() => setCats([]), [source, kind])
  const sorts = sortsFor(source)
  useEffect(() => {
    if (!sorts.some((s) => s.value === sort)) setSort('relevance')
  }, [sorts, sort])
  const [detail, setDetail] = useState<ModSearchHit | null>(null)
  const [install, setInstall] = useState<{ hit: ModSearchHit; version?: string } | null>(null)
  const [installing, setInstalling] = useState<string[]>([])
  const top = useRef<HTMLDivElement>(null)
  const goTo = (n: number) => {
    setPage(n)
    // A new page starts at the top, not where the last one was scrolled to.
    top.current?.scrollIntoView({ block: 'start' })
  }

  // Loaders narrow mods and modpacks; plugins run on any Bukkit-style server,
  // and SpigotMC can't be searched by Minecraft version.
  const byLoader = kind === 'mod' || kind === 'modpack'
  const byVersion = source !== 'spigotmc'
  const bySide = source === 'modrinth' && kind !== 'plugin'
  const versions = useQuery({ queryKey: ['mc-versions', server.platform], queryFn: () => api.minecraft.versions(server.platform), staleTime: 60 * 60_000, retry: false }).data
  const versionOptions = useMemo(() => {
    const list = (versions ?? []).map((v) => v.version)
    if (server.mc_version && !list.includes(server.mc_version)) list.unshift(server.mc_version)
    return [{ value: '', label: 'Any Minecraft version' }, ...list.map((v) => ({ value: v, label: v === server.mc_version ? `${v}, this server’s` : v }))]
  }, [versions, server.mc_version])
  const loaderOptions = [{ value: '', label: 'Any loader' }, ...LOADERS.map((l) => ({ value: l, label: l === server.platform ? `${platformLabel(l)}, this server’s` : platformLabel(l) }))]

  const categories = useQuery({ queryKey: ['mod-categories', server.id, kind, source], queryFn: () => api.mods.categories(server.id, kind, source), staleTime: 60 * 60_000, retry: false }).data ?? []
  // Modrinth narrows by every category picked; the others take one.
  const multiCat = source === 'modrinth'
  const toggleCat = (id: string) => setCats((c) => (c.includes(id) ? c.filter((x) => x !== id) : multiCat ? [...c, id] : [id]))
  const needsKey = source === 'curseforge' && sources?.curseforge === false
  const results = useQuery({
    queryKey: ['mod-search', server.id, source, kind, dq, cats, sort, mc, loader, side, page],
    queryFn: () =>
      api.mods.search(server.id, {
        query: dq,
        source,
        loader: byLoader ? loader || undefined : undefined,
        mcVersion: byVersion ? mc || undefined : undefined,
        projectType: kind,
        categories: cats.length ? cats : undefined,
        environment: bySide ? side || undefined : undefined,
        index: sort === 'relevance' && !dq && source === 'modrinth' ? 'downloads' : sort,
        limit: PAGE,
        offset: page * PAGE,
      }),
    staleTime: 60_000,
    placeholderData: (prev) => prev,
    enabled: !needsKey,
  })
  const total = results.data?.total_hits ?? null
  useEffect(() => onTotal(total), [total, onTotal])

  const installedIds = new Set(installed.map((m) => m.source_id).filter(Boolean))
  const catList = categories.filter((c) => c.header === 'categories' || !c.header)
  const catLabel = (name: string) => name.charAt(0).toUpperCase() + name.slice(1).replace(/-/g, ' ')
  const shown = (hits: ModSearchHit[]) => hits.filter((h) => (!hideMine || !installedIds.has(h.project_id)) && (!bySide || !contradicts(h, side)))
  const fits = (h: ModSearchHit) => fitsServer(h, kind, source, server.platform, server.mc_version)
  const filtered = (byVersion && mc !== server.mc_version) || (byLoader && loader !== (PLATFORMS[server.platform]?.content === 'mod' ? server.platform : '')) || cats.length > 0 || side !== '' || hideMine

  // Install in one click: the newest version made for this server. A modpack
  // replaces the server's mods, so it still asks first.
  const quickInstall = async (h: ModSearchHit) => {
    if (kind === 'modpack') {
      setInstall({ hit: h })
      return
    }
    setInstalling((x) => [...x, h.project_id])
    const r = await act(`install ${h.title}`, async () => {
      const list = await api.mods.getVersions(server.id, h.project_id, kind === 'mod' ? server.platform : undefined, source === 'spigotmc' ? undefined : server.mc_version, source)
      if (!list.length) return null
      await api.mods.install(server.id, source, h.project_id, list[0].id, true)
      return list[0]
    })
    setInstalling((x) => x.filter((id) => id !== h.project_id))
    if (r === undefined) return
    if (r === null) {
      toast('bad', `There’s no ${h.title} for ${platformLabel(server.platform)} ${server.mc_version}`, 'You can still pick a version made for another one.', {
        actions: [{ label: 'Pick a version', run: () => setInstall({ hit: h }) }],
      })
      return
    }
    toast('ok', `Installed ${h.title} ${r.version_number}`, status === 'online' ? 'Restart the server to load it.' : 'It loads the next time the server starts.')
    void qc.invalidateQueries({ queryKey: ['mods', server.id] })
  }

  const reset = () => {
    setMc(server.mc_version)
    setLoader(PLATFORMS[server.platform]?.content === 'mod' ? server.platform : '')
    setSide('')
    setCats([])
    setHideMine(false)
  }

  return (
    <>
      <div className="toolbar" ref={top} style={{ scrollMarginTop: 16 }}>
        <Search value={q} onChange={setQ} placeholder={`Search ${SOURCE_LABEL[source]}`} style={{ flex: '1 1 260px' }} autoFocus={finePointer()} />
        {kinds.length > 1 && <Select value={kind} onChange={setKind} options={kinds} style={{ width: 'auto' }} aria-label="Kind" />}
        {available.length > 1 && <Select value={source} onChange={setSource} options={available.map((s) => ({ value: s, label: SOURCE_LABEL[s] }))} style={{ width: 'auto' }} aria-label="Source" />}
        <Select value={sort} onChange={setSort} options={sorts} style={{ width: 'auto' }} aria-label="Sort" />
      </div>
      <div className="mod-filters">
        {byVersion && <Select value={mc} onChange={setMc} options={versionOptions} size="sm" aria-label="Minecraft version" />}
        {byLoader && <Select value={loader} onChange={setLoader} options={loaderOptions} size="sm" aria-label="Loader" />}
        {bySide && <Select value={side} onChange={setSide} options={SIDES} size="sm" aria-label="Where it runs" />}
        <Check checked={hideMine} onChange={setHideMine} label="Hide what’s installed" />
        {filtered && (
          <button type="button" className="link quiet small" onClick={reset}>
            Reset
          </button>
        )}
      </div>
      {catList.length > 0 && (
        <div className="mod-cats" role="group" aria-label="Categories">
          {catList.map((c) => {
            const id = c.id ?? c.name
            return (
              <button key={id} type="button" className="cat-chip" aria-pressed={cats.includes(id)} onClick={() => toggleCat(id)}>
                {catLabel(c.name)}
              </button>
            )
          })}
        </div>
      )}
      {needsKey ? (
        <Empty title="CurseForge needs an API key.">An administrator can add one in the panel’s settings. Installed CurseForge mods still update without it.</Empty>
      ) : (
        <Loaded query={results} sk={<SkItems rows={6} icons />} what="search results">
          {(r) => {
            const pages = Math.ceil(r.total_hits / PAGE)
            const nums = pageWindow(page, pages)
            const hits = shown(r.hits)
            return r.hits.length === 0 ? (
              <Empty title="Nothing matches.">{filtered ? 'Try fewer filters, or Reset them.' : 'Try another search.'}</Empty>
            ) : (
              <>
                <div className="items flush">
                  {hits.length === 0 && <p className="t2 small" style={{ padding: '14px 0' }}>Everything on this page is hidden by the filters above.</p>}
                  {hits.map((h) => {
                    const mine = installedIds.has(h.project_id)
                    const note = sideNote(h)
                    const tags = topicsOf(h.categories).slice(0, 4)
                    return (
                      <div key={h.project_id} className="item" style={{ alignItems: 'flex-start', gap: 14 }}>
                        <button type="button" className="mod-icon-btn" onClick={() => setDetail(h)} aria-label={`About ${h.title}`}>
                          <ModIcon url={h.icon_url} name={h.title} size={44} />
                        </button>
                        <div className="grow">
                          <button type="button" className="title link" style={{ textDecorationColor: 'transparent' }} onClick={() => setDetail(h)}>
                            {h.title}
                          </button>
                          <div className="t2 small">{h.description}</div>
                          <div className="meta">
                            {fmtCompact(h.downloads)} downloads{h.author ? `, by ${h.author}` : ''}.{' '}
                            {note && <span className={cx(note.warn && 'warn')}>{note.text} </span>}
                            {!mine && !fits(h) && <span className="warn">Not listed for {platformLabel(server.platform)} {server.mc_version}.</span>}
                          </div>
                          {tags.length > 0 && <div className="meta t3">{tags.map(catLabel).join(' · ')}</div>}
                        </div>
                        {mine ? (
                          <span className="t3 small" style={{ paddingTop: 4 }}>
                            Installed
                          </span>
                        ) : (
                          <Btn size="sm" need="mods.install" loading={installing.includes(h.project_id)} onClick={() => void quickInstall(h)}>
                            Install
                          </Btn>
                        )}
                      </div>
                    )
                  })}
                </div>
                {pages > 1 && (
                  <nav className="pager" aria-label="Pages">
                    <Btn size="sm" variant="quiet" disabled={page === 0} onClick={() => goTo(page - 1)}>
                      Previous
                    </Btn>
                    {nums[0] > 0 && <span className="t3">…</span>}
                    {nums.map((n) => (
                      <button key={n} type="button" className="page-n num" aria-current={n === page ? 'page' : undefined} onClick={() => goTo(n)}>
                        {n + 1}
                      </button>
                    ))}
                    {nums[nums.length - 1] < pages - 1 && <span className="t3">…</span>}
                    <Btn size="sm" variant="quiet" disabled={page >= pages - 1} onClick={() => goTo(page + 1)}>
                      Next
                    </Btn>
                    <span className="t3 small num">{fmtNum(r.total_hits)} found</span>
                  </nav>
                )}
              </>
            )
          }}
        </Loaded>
      )}
      {detail && (
        <ProjectSheet
          server={server}
          source={source}
          kind={kind}
          projectId={detail.project_id}
          hit={detail}
          installed={installedIds.has(detail.project_id)}
          installing={installing.includes(detail.project_id)}
          onClose={() => setDetail(null)}
          onInstall={() => void quickInstall(detail)}
          onChoose={(version) => {
            setInstall({ hit: detail, version })
            setDetail(null)
          }}
        />
      )}
      {install && <InstallDialog server={server} status={status} hit={install.hit} initial={install.version} source={source} kind={kind} onClose={() => setInstall(null)} />}
    </>
  )
}

// ---- a closer look --------------------------------------------------------------------------------

const MD: ComponentProps<typeof ReactMarkdown>['components'] = {
  a: ({ href, children }) => (
    <a className="link" href={href} target="_blank" rel="noopener noreferrer">
      {children}
    </a>
  ),
  img: ({ src, alt }) => <img src={typeof src === 'string' ? src : undefined} alt={alt ?? ''} loading="lazy" referrerPolicy="no-referrer" />,
}

/** A project's own text: Markdown, often with HTML mixed in, which is kept
 *  to what GitHub would allow (no scripts, styles or event handlers). */
export function Markdown({ text }: { text: string }) {
  return (
    <div className="md">
      <ReactMarkdown remarkPlugins={[remarkGfm]} rehypePlugins={[rehypeRaw, rehypeSanitize]} components={MD}>
        {text}
      </ReactMarkdown>
    </div>
  )
}

type SheetTab = 'about' | 'gallery' | 'versions'

/** A project up close: its description, pictures and every version. Opened
 *  from a search result, or from an installed mod with no hit to go on. */
export function ProjectSheet({
  server,
  source,
  kind,
  projectId,
  hit,
  installed,
  installing,
  onClose,
  onInstall,
  onChoose,
}: {
  server: Server
  source: ModSource
  kind: ModProjectType
  projectId: string
  hit?: ModSearchHit
  installed: boolean
  installing?: boolean
  onClose: () => void
  onInstall?: () => void
  onChoose?: (version?: string) => void
}) {
  const [tab, setTab] = useState<SheetTab>('about')
  const project = useQuery({ queryKey: ['mod-project', source, projectId], queryFn: () => api.mods.getProject(server.id, projectId, source), staleTime: 60 * 60_000, retry: false })
  const all = useQuery({
    queryKey: ['mod-versions', source, server.id, projectId, '', ''],
    queryFn: () => api.mods.getVersions(server.id, projectId, undefined, undefined, source),
    staleTime: 10 * 60_000,
    enabled: tab === 'versions',
  })
  const p = project.data
  const title = hit?.title ?? p?.title ?? 'Project'
  const sides = hit ?? p
  const note = sides ? sideNote(sides) : null
  const gallery = p?.gallery ?? []
  const loaderMatters = kind === 'mod' || kind === 'modpack'
  const fitsVersion = (gv: string[], loaders: string[]) => (!gv.length || gv.includes(server.mc_version)) && (!loaderMatters || !loaders.length || loaders.map((l) => l.toLowerCase()).includes(server.platform))
  return (
    <Sheet
      open
      onClose={onClose}
      wide
      icon={<ModIcon url={hit?.icon_url ?? p?.icon_url} name={title} size={44} />}
      title={title}
      sub={`${fmtCompact(hit?.downloads ?? p?.downloads ?? 0)} downloads${p?.updated ? `, updated ${relTime(toMs(p.updated))}` : ''}`}
      foot={
        installed ? (
          <span className="t2">Installed</span>
        ) : onInstall ? (
          <>
            <Btn variant="primary" need="mods.install" loading={installing} onClick={onInstall}>
              Install
            </Btn>
            {onChoose && (
              <TextBtn quiet need="mods.install" onClick={() => onChoose()}>
                Pick a version
              </TextBtn>
            )}
          </>
        ) : undefined
      }
    >
      <div className="stack" style={{ gap: 16 }}>
        <p style={{ fontSize: 15 }}>{p?.description ?? hit?.description}</p>
        <dl className="facts">
          <dt>Kind</dt>
          <dd>{topicsOf(p?.categories ?? hit?.categories ?? []).join(', ') || '–'}</dd>
          {note && (
            <>
              <dt>Runs on</dt>
              <dd className={cx(note.warn && 'warn')}>{note.text}</dd>
            </>
          )}
          <dt>From</dt>
          <dd>{SOURCE_LABEL[source]}</dd>
          {p?.followers != null && (
            <>
              <dt>Followers</dt>
              <dd className="num">{fmtNum(p.followers)}</dd>
            </>
          )}
        </dl>
        {(p?.source_url || p?.issues_url || p?.wiki_url) && (
          <p className="small row" style={{ gap: 16 }}>
            {p.source_url && (
              <a className="link quiet" href={p.source_url} target="_blank" rel="noopener noreferrer">
                Source
              </a>
            )}
            {p.issues_url && (
              <a className="link quiet" href={p.issues_url} target="_blank" rel="noopener noreferrer">
                Issues
              </a>
            )}
            {p.wiki_url && (
              <a className="link quiet" href={p.wiki_url} target="_blank" rel="noopener noreferrer">
                Wiki
              </a>
            )}
          </p>
        )}
        <Tabs
          inset
          value={tab}
          onChange={setTab}
          items={[
            { value: 'about', label: 'Description' },
            { value: 'gallery', label: 'Gallery', count: gallery.length || undefined, disabled: !gallery.length },
            { value: 'versions', label: 'Versions' },
          ]}
        />
        {tab === 'about' && (
          <Loaded query={project} sk={<SheetSkeleton />} what="its description">
            {(proj) => (proj.body ? <Markdown text={proj.body} /> : <p className="t3">It has no longer description.</p>)}
          </Loaded>
        )}
        {tab === 'gallery' && (
          <div className="mod-gallery">
            {gallery.map((g) => (
              <figure key={g.url}>
                <a href={g.url} target="_blank" rel="noopener noreferrer">
                  <img src={g.url} alt={g.title || ''} loading="lazy" referrerPolicy="no-referrer" />
                </a>
                {(g.title || g.description) && (
                  <figcaption className="small t2">
                    {g.title && <span className="strong">{g.title}</span>} {g.description}
                  </figcaption>
                )}
              </figure>
            ))}
          </div>
        )}
        {tab === 'versions' && (
          <Loaded query={all} sk={<SkItems rows={5} />} what="its versions">
            {(list) =>
              list.length === 0 ? (
                <Empty title="No versions to download." />
              ) : (
                <div className="items flush">
                  {list.slice(0, 60).map((v) => {
                    const ok = fitsVersion(v.game_versions, v.loaders)
                    return (
                      <div key={v.id} className="item" style={{ alignItems: 'flex-start' }}>
                        <div className="grow">
                          <div className="title">
                            {v.version_number}
                            {v.version_type && v.version_type !== 'release' && <span className="warn small"> {v.version_type}</span>}
                          </div>
                          <div className="meta">
                            {fmtDate(toMs(v.date_published) ?? 0, false)}, for {gameVersions(v.game_versions, server.mc_version)}
                            {v.loaders.length ? ` on ${v.loaders.map(platformLabel).join(', ')}` : ''}.{' '}
                            {!ok && <span className="warn">Not for this server.</span>}
                          </div>
                        </div>
                        {!installed && onChoose && (
                          <TextBtn quiet need="mods.install" onClick={() => onChoose(v.id)}>
                            Install
                          </TextBtn>
                        )}
                      </div>
                    )
                  })}
                </div>
              )
            }
          </Loaded>
        )}
      </div>
    </Sheet>
  )
}

function InstallDialog({ server, status, hit, initial, source, kind, onClose }: { server: Server; status: StatusKey; hit: ModSearchHit; initial?: string; source: ModSource; kind: ModProjectType; onClose: () => void }) {
  const qc = useQueryClient()
  const can = useCan()
  const [sel, setSel] = useState(initial ?? '')
  const [busy, setBusy] = useState(false)
  // A version picked from the full list may be for another Minecraft version.
  const [anyVersion, setAnyVersion] = useState(!!initial)
  const loader = kind === 'mod' || kind === 'modpack' ? server.platform : undefined
  const mc = anyVersion || source === 'spigotmc' ? undefined : server.mc_version
  const versions = useQuery({
    queryKey: ['mod-versions', source, server.id, hit.project_id, anyVersion ? '' : (loader ?? ''), mc ?? ''],
    queryFn: () => api.mods.getVersions(server.id, hit.project_id, anyVersion ? undefined : loader, mc, source),
    staleTime: 10 * 60_000,
  })
  useEffect(() => {
    if (versions.data?.length && !versions.data.some((v) => v.id === sel)) setSel(versions.data[0].id)
  }, [versions.data, sel])
  const modpack = kind === 'modpack'
  const install = async () => {
    if (!sel) return
    setBusy(true)
    const r = await act(`install ${hit.title}`, () => (modpack ? api.mods.installModpack(server.id, hit.project_id, sel) : api.mods.install(server.id, source, hit.project_id, sel, true)).then(() => true))
    setBusy(false)
    if (r === undefined) return
    toast('ok', `Installed ${hit.title}`, status === 'online' ? 'Restart the server to load it.' : 'It loads the next time the server starts.')
    void qc.invalidateQueries({ queryKey: ['mods', server.id] })
    onClose()
  }
  const options = useMemo(
    () =>
      (versions.data ?? []).slice(0, 60).map((v, i) => ({
        value: v.id,
        label: `${v.version_number}${v.version_type && v.version_type !== 'release' ? ` (${v.version_type})` : ''}${i === 0 ? ', newest' : ''}${anyVersion ? `, for ${gameVersions(v.game_versions, server.mc_version)}${v.loaders.length ? ` on ${v.loaders.map(platformLabel).join(', ')}` : ''}` : ''}`,
        hint: fmtDate(toMs(v.date_published) ?? 0, false),
      })),
    [versions.data, anyVersion, server.mc_version],
  )
  return (
    <Dialog
      open
      onClose={onClose}
      title={`Install ${hit.title}`}
      foot={
        <>
          <Btn variant="quiet" onClick={onClose}>
            Cancel
          </Btn>
          <Btn variant="primary" disabled={!sel || !can('mods.install')} loading={busy} onClick={() => void install()}>
            Install
          </Btn>
        </>
      }
    >
      <div className="stack" style={{ gap: 14 }}>
        <div className="row" style={{ gap: 12 }}>
          <ModIcon url={hit.icon_url} name={hit.title} size={40} />
          <p className="t2 small grow">{hit.description}</p>
        </div>
        <Loaded query={versions} sk={<SkItems rows={1} />} what="its versions">
          {(list) =>
            list.length === 0 ? (
              <Notice tone="warn">
                There’s no version of {hit.title} for {platformLabel(server.platform)} {server.mc_version}.
              </Notice>
            ) : (
              <Field label="Version">
                <Select value={sel} onChange={setSel} options={options} aria-label="Version" />
              </Field>
            )
          }
        </Loaded>
        <Check checked={anyVersion} onChange={setAnyVersion} label="Show versions for other loaders and Minecraft versions too" />
        {anyVersion && <p className="small warn">One made for another loader or Minecraft version may not load on this server.</p>}
        {modpack ? (
          <p className="t2 small">A modpack replaces this server’s mods with its own. Back up first if you might want them back.</p>
        ) : (
          <p className="t2 small">Anything it needs is installed with it.</p>
        )}
        {hit.client_side === 'required' && <p className="t2 small">Players will need {hit.title} on their side as well.</p>}
      </div>
    </Dialog>
  )
}
