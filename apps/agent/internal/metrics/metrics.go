package metrics

import (
	"runtime"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/net"
	"github.com/shirou/gopsutil/v4/process"
)

type HostStats struct {
	CPUPct      float64 `json:"cpu_pct"`
	RAMMb       uint64  `json:"ram_mb"`
	RAMTotalMb  uint64  `json:"ram_total_mb"`
	DiskUsedGb  float64 `json:"disk_used_gb"`
	DiskTotalGb float64 `json:"disk_total_gb"`
}

type ProcessStats struct {
	CPUPct   float64 `json:"cpu_pct"`
	RAMMb    uint64  `json:"ram_mb"`
	NetRxBps uint64  `json:"net_rx_bps"`
	NetTxBps uint64  `json:"net_tx_bps"`
}

type cpuSample struct {
	total float64 // cumulative CPU seconds (user+system)
	at    time.Time
}

type cachedHostStats struct {
	at    time.Time
	stats HostStats
}

type cachedProcessStats struct {
	at    time.Time
	stats ProcessStats
}

// A metrics frame is emitted every two seconds. Reusing a sample for one
// second collapses near-simultaneous dashboard, header, and API sampler reads
// without reducing the visible resolution.
const sampleCacheTTL = time.Second

type Collector struct {
	netMu    sync.Mutex
	prevRx   uint64
	prevTx   uint64
	prevTime time.Time
	netAt    time.Time
	netRxBps uint64
	netTxBps uint64

	cpuMu   sync.Mutex
	prevCPU map[int32]cpuSample // per-pid, for instantaneous CPU%

	hostMu    sync.Mutex
	hostCache map[string]cachedHostStats // keyed by data directory (disk stats differ)

	processMu    sync.Mutex
	processCache map[int32]cachedProcessStats
}

func NewCollector() *Collector {
	return &Collector{
		prevCPU:      map[int32]cpuSample{},
		hostCache:    map[string]cachedHostStats{},
		processCache: map[int32]cachedProcessStats{},
	}
}

// procCPUPercent returns instantaneous CPU% for a process, normalized so that
// 100% means all logical cores saturated (matches host CPU semantics). Uses the
// delta in cumulative CPU time since the previous call for this pid.
func (c *Collector) procCPUPercent(proc *process.Process) float64 {
	times, err := proc.Times()
	if err != nil {
		return 0
	}
	total := times.User + times.System
	now := time.Now()

	c.cpuMu.Lock()
	defer c.cpuMu.Unlock()

	prev, ok := c.prevCPU[proc.Pid]
	c.prevCPU[proc.Pid] = cpuSample{total: total, at: now}
	if !ok {
		return 0 // first sample establishes a baseline
	}

	elapsed := now.Sub(prev.at).Seconds()
	if elapsed <= 0 {
		return 0
	}
	pct := (total - prev.total) / elapsed * 100
	if n := runtime.NumCPU(); n > 0 {
		pct /= float64(n)
	}
	if pct < 0 {
		pct = 0
	}
	return pct
}

func (c *Collector) Host(dataDir string) (*HostStats, error) {
	c.hostMu.Lock()
	defer c.hostMu.Unlock()

	now := time.Now()
	if cached, ok := c.hostCache[dataDir]; ok && now.Sub(cached.at) < sampleCacheTTL {
		stats := cached.stats
		return &stats, nil
	}

	pcts, err := cpu.Percent(0, false)
	if err != nil {
		return nil, err
	}
	cpuPct := 0.0
	if len(pcts) > 0 {
		cpuPct = pcts[0]
	}

	vm, err := mem.VirtualMemory()
	if err != nil {
		return nil, err
	}

	du, err := disk.Usage(dataDir)
	if err != nil {
		du = &disk.UsageStat{}
	}

	stats := HostStats{
		CPUPct:      cpuPct,
		RAMMb:       vm.Used / 1024 / 1024,
		RAMTotalMb:  vm.Total / 1024 / 1024,
		DiskUsedGb:  float64(du.Used) / 1024 / 1024 / 1024,
		DiskTotalGb: float64(du.Total) / 1024 / 1024 / 1024,
	}
	c.hostCache[dataDir] = cachedHostStats{at: now, stats: stats}
	return &stats, nil
}

func (c *Collector) Process(pid int32) (*ProcessStats, error) {
	if pid <= 0 {
		return &ProcessStats{}, nil
	}

	// Serialize cache misses as well as cache access. OS process and network
	// counters are relatively expensive, and concurrent readers of one PID must
	// share the same CPU baseline rather than perturbing one another.
	c.processMu.Lock()
	defer c.processMu.Unlock()

	now := time.Now()
	if cached, ok := c.processCache[pid]; ok && now.Sub(cached.at) < sampleCacheTTL {
		stats := cached.stats
		return &stats, nil
	}

	proc, err := process.NewProcess(pid)
	if err != nil {
		return &ProcessStats{}, nil
	}

	cpuPct := c.procCPUPercent(proc)

	mi, err := proc.MemoryInfo()
	var ramMb uint64
	if err == nil && mi != nil {
		ramMb = mi.RSS / 1024 / 1024
	}

	rxBps, txBps := c.networkRates(now)
	stats := ProcessStats{
		CPUPct:   cpuPct,
		RAMMb:    ramMb,
		NetRxBps: rxBps,
		NetTxBps: txBps,
	}
	c.processCache[pid] = cachedProcessStats{at: now, stats: stats}
	c.pruneProcessState(now)
	return &stats, nil
}

// networkRates samples host network counters at most once per cache interval.
// The counters available here are host-wide; sharing one rate across process
// reads avoids the old first-caller-wins behavior where the first server got
// the delta and every server sampled milliseconds later got zero.
func (c *Collector) networkRates(now time.Time) (uint64, uint64) {
	c.netMu.Lock()
	defer c.netMu.Unlock()

	if !c.netAt.IsZero() && now.Sub(c.netAt) < sampleCacheTTL {
		return c.netRxBps, c.netTxBps
	}

	counters, err := net.IOCounters(false)
	if err != nil || len(counters) == 0 {
		return 0, 0
	}

	var rxBps, txBps uint64
	if !c.prevTime.IsZero() {
		elapsed := now.Sub(c.prevTime).Seconds()
		rxBps = counterRate(c.prevRx, counters[0].BytesRecv, elapsed)
		txBps = counterRate(c.prevTx, counters[0].BytesSent, elapsed)
	}
	c.prevRx = counters[0].BytesRecv
	c.prevTx = counters[0].BytesSent
	c.prevTime = now
	c.netAt = now
	c.netRxBps = rxBps
	c.netTxBps = txBps
	return rxBps, txBps
}

func counterRate(previous, current uint64, elapsedSeconds float64) uint64 {
	if elapsedSeconds <= 0 || current < previous {
		return 0
	}
	return uint64(float64(current-previous) / elapsedSeconds)
}

// Long-running agents can see many PIDs as JVMs restart. Bound both caches so
// dead-process entries do not accumulate for the lifetime of the agent.
func (c *Collector) pruneProcessState(now time.Time) {
	const staleAfter = 10 * time.Minute
	if len(c.processCache) > 128 {
		for pid, cached := range c.processCache {
			if now.Sub(cached.at) > staleAfter {
				delete(c.processCache, pid)
			}
		}
	}

	c.cpuMu.Lock()
	defer c.cpuMu.Unlock()
	if len(c.prevCPU) > 128 {
		for pid, sample := range c.prevCPU {
			if now.Sub(sample.at) > staleAfter {
				delete(c.prevCPU, pid)
			}
		}
	}
}
