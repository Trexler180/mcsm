import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { api } from '@/lib/api'
import type { MetricsHistory, Server, ServerStats } from '@/lib/types'
import { useGo } from '@/shell/nav'
import { paths } from '@/shell/paths'
import { Columns, Heatmap, LineChart, UptimeStrip, type Point } from '@/ui/charts'
import { Empty, Filters, TextBtn } from '@/ui/controls'
import { DOW, fmtClock, fmtDate, fmtDay, fmtDur, fmtMB, fmtNum } from '@/ui/format'
import { PlayerHead } from '@/ui/pixel'
import { Loaded, StatsSkeleton } from '@/ui/skeleton'
import { DAY, plural } from '@/ui/util'

// Stats: who plays and when, how smoothly the server runs, and how often it
// was up, over a period.

type Period = 1 | 7 | 30 | 90 | 0

export function StatsTab({ server }: { server: Server }) {
  const [days, setDays] = useState<Period>(7)
  const stats = useQuery({ queryKey: ['server-stats', server.id, days], queryFn: () => api.servers.stats(server.id, days), staleTime: 60_000 })
  const history = useQuery({ queryKey: ['metrics-history', server.id, days * 24], queryFn: () => api.servers.metricsHistory(server.id, days * 24), staleTime: 60_000 })
  return (
    <>
      <div className="toolbar" style={{ marginBottom: 24 }}>
        <Filters
          value={String(days)}
          onChange={(v) => setDays(Number(v) as Period)}
          label="Period"
          options={[
            { value: '1', label: 'Today' },
            { value: '7', label: '7 days' },
            { value: '30', label: '30 days' },
            { value: '90', label: '90 days' },
            { value: '0', label: 'All time' },
          ]}
        />
      </div>
      <Loaded query={stats} sk={<StatsSkeleton />} what={`stats for ${server.name}`}>
        {(s) => <StatsBody server={server} days={days} stats={s} history={history.data} />}
      </Loaded>
    </>
  )
}

function StatsBody({ server, days, stats, history }: { server: Server; days: Period; stats: ServerStats; history: MetricsHistory | undefined }) {
  const go = useGo()
  const [asTable, setAsTable] = useState(false)
  const short = days === 1
  const xFmt = short ? (t: number) => fmtClock(t) : (t: number) => fmtDate(t, false)
  const tip = (d: Point) => (short ? fmtClock(d.ts) : fmtDay(d.ts) + ' ' + fmtClock(d.ts))
  const pts = history?.points ?? []
  const online: Point[] = pts.map((p) => ({ ts: p.ts * 1000, players: p.players, cpu: p.cpu_percent, ram: p.ram_used_mb }))
  const ticks: Point[] = pts.filter((p) => p.tps != null).map((p) => ({ ts: p.ts * 1000, tps: p.tps as number }))
  const mspt: Point[] = pts.filter((p) => p.mspt_avg != null).map((p) => ({ ts: p.ts * 1000, mspt: p.mspt_avg as number, mspt_p95: (p.mspt_p95 ?? p.mspt_avg) as number }))
  const sum = stats.summary
  const up = stats.uptime
  const peakAt = sum.peak_ts ? sum.peak_ts * 1000 : null
  const daily = stats.daily.map((d) => ({ ...d, ts: Date.parse(d.date + 'T12:00:00'), v: d.unique_players, label: d.date }))
  const top = stats.top_players.slice(0, 8)
  const from = stats.since ? stats.since * 1000 : Date.now() - (days || 30) * DAY
  if (!stats.data_since) {
    return <Empty title="Nothing recorded yet.">Stats build up while {server.name} runs: who joins, how long they stay, and how smoothly it ticks.</Empty>
  }
  return (
    <>
      <div className="figures">
        <div className="figure">
          <div className="v">{fmtNum(sum.unique_players)}</div>
          <div className="l">{sum.unique_players === 1 ? 'player' : 'different players'}</div>
        </div>
        <div className="figure">
          <div className="v">{fmtNum(sum.peak_players)}</div>
          <div className="l">{peakAt && sum.peak_players > 0 ? `at once, ${fmtDay(peakAt) === 'Today' ? 'today' : fmtDay(peakAt)} ${fmtClock(peakAt)}` : 'at once at most'}</div>
        </div>
        <div className="figure">
          <div className="v">{fmtDur(sum.playtime_seconds * 1000, 1)}</div>
          <div className="l">played in total</div>
        </div>
        <div className="figure">
          <div className="v">{up.tracked_since ? `${up.availability_pct.toFixed(1)}%` : '–'}</div>
          <div className="l">{up.tracked_since ? `up, ${plural(up.crashes, 'crash', 'crashes')}` : 'up time not tracked yet'}</div>
        </div>
      </div>

      <div className="chart-block">
        <div className="chart-title">
          <h3>People online</h3>
        </div>
        <LineChart data={online} k="players" integer xFmt={xFmt} tipTitle={tip} height={200} label="People online" />
      </div>

      <div className="chart-block split" style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(360px, 1fr))', gap: '36px 48px' }}>
        <div>
          <div className="chart-title">
            <h3>When people play</h3>
            <span className="aside">Average by hour</span>
          </div>
          <Heatmap cells={stats.activity.map((c) => ({ dow: c.dow, hour: c.hour, avg: c.avg_players }))} />
        </div>
        <div>
          <div className="chart-title">
            <h3>Players each day</h3>
            <TextBtn quiet className="small" onClick={() => setAsTable(!asTable)}>
              {asTable ? 'Show the chart' : 'Show as a table'}
            </TextBtn>
          </div>
          {asTable ? (
            <div style={{ maxHeight: 200, overflowY: 'auto' }}>
              <table className="t">
                <thead>
                  <tr>
                    <th>Day</th>
                    <th className="r">Players</th>
                    <th className="r">Joins</th>
                  </tr>
                </thead>
                <tbody>
                  {daily
                    .slice()
                    .reverse()
                    .map((d) => (
                      <tr key={d.date}>
                        <td>{fmtDay(d.ts)}</td>
                        <td className="r num">{d.unique_players}</td>
                        <td className="r num t2">{d.joins}</td>
                      </tr>
                    ))}
                </tbody>
              </table>
            </div>
          ) : (
            <Columns
              data={daily.slice(-30)}
              xFmt={(d) => (daily.length <= 7 ? DOW[new Date(d.ts).getDay()] : String(new Date(d.ts).getDate()))}
              tip={(d) => fmtDay(d.ts)}
              label="Players each day"
              height={180}
            />
          )}
        </div>
      </div>

      <div className="chart-block split" style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(360px, 1fr))', gap: '36px 48px' }}>
        <div>
          <div className="chart-title">
            <h3>Ticks per second</h3>
            <span className="aside">20 is perfect. The red line is where players start to notice.</span>
          </div>
          {ticks.length > 1 ? (
            <LineChart data={ticks} k="tps" yMin={0} yMax={20} yFmt={(v) => v.toFixed(0)} refLine={{ v: 18.5 }} xFmt={xFmt} tipTitle={tip} height={170} label="Ticks per second" />
          ) : (
            <NoTicks />
          )}
        </div>
        <div>
          <div className="chart-title">
            <h3>Time per tick</h3>
            <span className="aside">Has to stay under the red line, 50 ms</span>
          </div>
          {mspt.length > 1 ? (
            <>
              <LineChart
                data={mspt}
                k="mspt"
                k2="mspt_p95"
                kLabel="typical"
                k2Label="slowest 5%"
                yFmt={(v) => Math.round(v) + ' ms'}
                refLine={{ v: 50 }}
                xFmt={xFmt}
                tipTitle={tip}
                height={170}
                label="Milliseconds per tick, typical and slowest 5%"
              />
              <div className="chart-legend">
                <span>
                  <i className="sw" />
                  Typical tick
                </span>
                <span>
                  <i className="sw faint" />
                  Slowest 5%. Spikes here are the stutters players feel.
                </span>
              </div>
            </>
          ) : (
            <NoTicks />
          )}
        </div>
      </div>

      <div className="chart-block">
        <div className="chart-title">
          <h3>Up time</h3>
          {up.longest_uptime_seconds > 0 && <span className="aside">Longest run {fmtDur(up.longest_uptime_seconds * 1000, 1)}</span>}
        </div>
        {up.tracked_since ? (
          <UptimeStrip
            segs={up.segments.map((g) => ({ start: g.started_at * 1000, end: g.ended_at ? g.ended_at * 1000 : null, reason: g.end_reason === 'crash' ? 'crash' : undefined }))}
            from={Math.max(from, up.tracked_since * 1000)}
            to={Date.now()}
          />
        ) : (
          <p className="t3 small">The panel starts tracking up time the first time it sees {server.name} start.</p>
        )}
      </div>

      <div className="chart-block split" style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(360px, 1fr))', gap: '36px 48px' }}>
        <div>
          <div className="chart-title">
            <h3>CPU</h3>
          </div>
          <LineChart data={online} k="cpu" yFmt={(v) => Math.round(v) + '%'} xFmt={xFmt} tipTitle={tip} height={140} label="CPU" />
          <div className="chart-title" style={{ marginTop: 26 }}>
            <h3>Memory</h3>
          </div>
          <LineChart data={online} k="ram" yMax={server.ram_mb_max} yFmt={(v) => fmtMB(v)} xFmt={xFmt} tipTitle={tip} height={140} label="Memory" />
        </div>
        <div>
          <div className="chart-title">
            <h3>Who plays most</h3>
            <span className="aside">{days ? `Last ${days === 1 ? 'day' : `${days} days`}` : 'All time'}</span>
          </div>
          {top.length ? (
            <table className="t">
              <tbody>
                {top.map((p) => (
                  <tr key={p.name} className="click" onClick={() => go(paths.server(server.id, 'players') + `?player=${encodeURIComponent(p.name)}`)}>
                    <td>
                      <span className="row" style={{ gap: 10 }}>
                        <PlayerHead name={p.name} size={18} />
                        {p.name}
                        {p.online && <span className="t3 small">online</span>}
                      </span>
                    </td>
                    <td style={{ width: '40%' }}>
                      <div className="meter">
                        <i style={{ width: (p.playtime_seconds / Math.max(1, top[0].playtime_seconds)) * 100 + '%' }} />
                      </div>
                    </td>
                    <td className="r num t2" style={{ width: 70 }}>
                      {fmtDur(p.playtime_seconds * 1000, 1)}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          ) : (
            <p className="t3 small">Nobody has played in this period.</p>
          )}
        </div>
      </div>
    </>
  )
}

function NoTicks() {
  return (
    <div className="chart t3 small" style={{ height: 170, display: 'grid', placeItems: 'center', textAlign: 'center', padding: '0 24px' }}>
      Tick timings come from the MCSM helper mod, which runs on Fabric servers. They show up here once it’s installed and the server has run for a while.
    </div>
  )
}
