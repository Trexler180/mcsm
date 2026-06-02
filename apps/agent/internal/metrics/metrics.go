package metrics

import (
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

type Collector struct {
	mu       sync.Mutex
	prevRx   uint64
	prevTx   uint64
	prevTime time.Time
}

func NewCollector() *Collector {
	return &Collector{prevTime: time.Now()}
}

func (c *Collector) Host(dataDir string) (*HostStats, error) {
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

	return &HostStats{
		CPUPct:      cpuPct,
		RAMMb:       vm.Used / 1024 / 1024,
		RAMTotalMb:  vm.Total / 1024 / 1024,
		DiskUsedGb:  float64(du.Used) / 1024 / 1024 / 1024,
		DiskTotalGb: float64(du.Total) / 1024 / 1024 / 1024,
	}, nil
}

func (c *Collector) Process(pid int32) (*ProcessStats, error) {
	if pid <= 0 {
		return &ProcessStats{}, nil
	}
	proc, err := process.NewProcess(pid)
	if err != nil {
		return &ProcessStats{}, nil
	}

	cpuPct, err := proc.CPUPercent()
	if err != nil {
		cpuPct = 0
	}

	mi, err := proc.MemoryInfo()
	var ramMb uint64
	if err == nil && mi != nil {
		ramMb = mi.RSS / 1024 / 1024
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	counters, err := net.IOCounters(false)
	var rxBps, txBps uint64
	if err == nil && len(counters) > 0 {
		now := time.Now()
		elapsed := now.Sub(c.prevTime).Seconds()
		if elapsed > 0 && c.prevTime.Unix() > 0 {
			rxDelta := counters[0].BytesRecv - c.prevRx
			txDelta := counters[0].BytesSent - c.prevTx
			rxBps = uint64(float64(rxDelta) / elapsed)
			txBps = uint64(float64(txDelta) / elapsed)
		}
		c.prevRx = counters[0].BytesRecv
		c.prevTx = counters[0].BytesSent
		c.prevTime = now
	}

	return &ProcessStats{
		CPUPct:   cpuPct,
		RAMMb:    ramMb,
		NetRxBps: rxBps,
		NetTxBps: txBps,
	}, nil
}
