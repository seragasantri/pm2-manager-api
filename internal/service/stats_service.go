package service

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

type StatsService struct{}

func NewStatsService() *StatsService { return &StatsService{} }

type ServerStats struct {
	CPU           float64 `json:"cpu"`
	LoadAvg       []float64 `json:"loadAvg"`
	MemoryUsed    uint64  `json:"memoryUsed"`
	MemoryTotal   uint64  `json:"memoryTotal"`
	MemoryPercent float64 `json:"memoryPercent"`
	DiskUsed      uint64  `json:"diskUsed"`
	DiskTotal     uint64  `json:"diskTotal"`
	DiskPercent   float64 `json:"diskPercent"`
	Uptime        float64 `json:"uptime"`
	UptimeSince   string  `json:"uptimeSince"`
	CPUCores      int     `json:"cpuCores"`
	NetRx         uint64  `json:"netRx"` // total byte diterima (interface fisik)
	NetTx         uint64  `json:"netTx"` // total byte dikirim
	SwapUsed      uint64  `json:"swapUsed"`
	SwapTotal     uint64  `json:"swapTotal"`
	Timestamp     int64   `json:"timestamp"` // epoch ms, untuk menghitung laju jaringan
}

func (s *StatsService) GetStats() (*ServerStats, error) {
	stats := &ServerStats{}

	// Memory from /proc/meminfo
	if f, err := os.Open("/proc/meminfo"); err == nil {
		scanner := bufio.NewScanner(f)
		var total, free, available, swapTotal, swapFree uint64
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "MemTotal:") {
				total = parseMemKB(line)
			} else if strings.HasPrefix(line, "MemAvailable:") {
				available = parseMemKB(line)
			} else if strings.HasPrefix(line, "MemFree:") {
				free = parseMemKB(line)
			} else if strings.HasPrefix(line, "SwapTotal:") {
				swapTotal = parseMemKB(line)
			} else if strings.HasPrefix(line, "SwapFree:") {
				swapFree = parseMemKB(line)
			}
		}
		f.Close()
		if total > 0 {
			if available > 0 {
				stats.MemoryTotal = total * 1024
				stats.MemoryUsed = (total - available) * 1024
			} else {
				stats.MemoryTotal = total * 1024
				stats.MemoryUsed = (total - free) * 1024
			}
			stats.MemoryPercent = float64(stats.MemoryUsed) / float64(stats.MemoryTotal) * 100
		}
		stats.SwapTotal = swapTotal * 1024
		stats.SwapUsed = (swapTotal - swapFree) * 1024
	}

	// Load avg from /proc/loadavg
	if data, err := os.ReadFile("/proc/loadavg"); err == nil {
		parts := strings.Fields(string(data))
		for i := 0; i < 3 && i < len(parts); i++ {
			if v, err := strconv.ParseFloat(parts[i], 64); err == nil {
				stats.LoadAvg = append(stats.LoadAvg, v)
			}
		}
	}

	// CPU from /proc/stat (snapshot diff needs 2 reads, use 100ms interval)
	stats.CPU = readCPUPercent()
	if len(stats.LoadAvg) > 0 && stats.LoadAvg[0] > 0 {
		// Normalize CPU approx from load if needed, but prefer /proc/stat
	}

	// Disk from syscall
	var fs syscall.Statfs_t
	if err := syscall.Statfs("/", &fs); err == nil {
		total := fs.Blocks * uint64(fs.Bsize)
		free := fs.Bavail * uint64(fs.Bsize)
		stats.DiskTotal = total
		stats.DiskUsed = total - free
		if total > 0 {
			stats.DiskPercent = float64(stats.DiskUsed) / float64(total) * 100
		}
	}

	// Uptime from /proc/uptime
	if data, err := os.ReadFile("/proc/uptime"); err == nil {
		parts := strings.Fields(string(data))
		if len(parts) > 0 {
			if v, err := strconv.ParseFloat(parts[0], 64); err == nil {
				stats.Uptime = v
				since := time.Now().Add(-time.Duration(v * float64(time.Second)))
				stats.UptimeSince = since.Format("2006-01-02 15:04:05")
			}
		}
	}

	_, stats.CPUCores = readCPUInfo()
	for name, v := range ReadNetDev() {
		if isVirtualIface(name) {
			continue
		}
		stats.NetRx += v[0]
		stats.NetTx += v[1]
	}
	stats.Timestamp = time.Now().UnixMilli()

	return stats, nil
}

func parseMemKB(line string) uint64 {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return 0
	}
	v, _ := strconv.ParseUint(fields[1], 10, 64)
	return v
}

func readCPUPercent() float64 {
	readStat := func() (total, idle uint64) {
		data, err := os.ReadFile("/proc/stat")
		if err != nil {
			return 0, 0
		}
		for _, line := range strings.Split(string(data), "\n") {
			if !strings.HasPrefix(line, "cpu ") {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) < 5 {
				continue
			}
			for i, f := range fields[1:] {
				v, _ := strconv.ParseUint(f, 10, 64)
				total += v
				if i == 3 || i == 4 { // idle + iowait
					idle += v
				}
			}
			break
		}
		return total, idle
	}

	// Bandingkan dengan snapshot request sebelumnya (dashboard memanggil tiap
	// ~3 detik) → rata-rata yang stabil, bukan sampel sesaat 80 ms yang
	// melonjak ke 100% saat ada proses singkat. Snapshot lama/terlalu baru →
	// ambil sampel 250 ms.
	cpuSnap.Lock()
	prev := cpuSnap.snap
	cpuSnap.Unlock()
	total2, idle2 := readStat()
	if total2 == 0 {
		return 0
	}
	total1, idle1 := prev.total, prev.idle
	if age := time.Since(prev.at); prev.total == 0 || age < 500*time.Millisecond || age > 30*time.Second || total2 <= total1 {
		total1, idle1 = total2, idle2
		time.Sleep(250 * time.Millisecond)
		total2, idle2 = readStat()
	}
	cpuSnap.Lock()
	cpuSnap.snap = cpuSample{total: total2, idle: idle2, at: time.Now()}
	cpuSnap.Unlock()
	dTotal := float64(total2 - total1)
	dIdle := float64(idle2 - idle1)
	if dTotal == 0 {
		return 0
	}
	pct := (1 - dIdle/dTotal) * 100
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	return pct
}

var _ = fmt.Sprintf // keep import if unused

type cpuSample struct {
	total, idle uint64
	at          time.Time
}

// cpuSnap menyimpan snapshot /proc/stat terakhir antar-request.
var cpuSnap struct {
	sync.Mutex
	snap cpuSample
}
