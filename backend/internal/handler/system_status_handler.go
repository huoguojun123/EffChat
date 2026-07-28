package handler

import (
	"context"
	"database/sql"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/effchat/internal/extractor"
	"github.com/huoguojun123/effchat/internal/filepolicy"
	"github.com/huoguojun123/effchat/pkg/config"
)

const statusProbeTimeout = 2 * time.Second

type systemRuntimeStatus struct {
	GoVersion                 string  `json:"go_version"`
	CPUCount                  int     `json:"cpu_count"`
	Goroutines                int     `json:"goroutines"`
	HeapAllocBytes            uint64  `json:"heap_alloc_bytes"`
	ContainerMemoryUsedBytes  *uint64 `json:"container_memory_used_bytes,omitempty"`
	ContainerMemoryLimitBytes *uint64 `json:"container_memory_limit_bytes,omitempty"`
}

type systemStorageStatus struct {
	TotalBytes uint64 `json:"total_bytes"`
	FreeBytes  uint64 `json:"free_bytes"`
}

type systemDatabaseStatus struct {
	OK               bool  `json:"ok"`
	LatencyMS        int64 `json:"latency_ms"`
	OpenConnections  int   `json:"open_connections"`
	InUseConnections int   `json:"in_use_connections"`
	IdleConnections  int   `json:"idle_connections"`
}

type systemExtractorStatus struct {
	Enabled   bool  `json:"enabled"`
	OK        bool  `json:"ok"`
	LatencyMS int64 `json:"latency_ms"`
}

type systemStatusResponse struct {
	Version       string                `json:"version"`
	BuildRef      string                `json:"build_ref"`
	SchemaVersion string                `json:"schema_version"`
	StartedAt     time.Time             `json:"started_at"`
	UptimeSeconds int64                 `json:"uptime_seconds"`
	Runtime       systemRuntimeStatus   `json:"runtime"`
	Storage       systemStorageStatus   `json:"storage"`
	Database      systemDatabaseStatus  `json:"database"`
	Extractor     systemExtractorStatus `json:"extractor"`
}

func AdminSystemStatusHandler(database *sql.DB, extractorClient *extractor.SidecarClient, startedAt time.Time) gin.HandlerFunc {
	return func(c *gin.Context) {
		response := systemStatusResponse{
			Version:       config.AppVersion,
			BuildRef:      config.BuildRef,
			StartedAt:     startedAt.UTC(),
			UptimeSeconds: max(0, int64(time.Since(startedAt).Seconds())),
			Runtime:       collectRuntimeStatus(),
			Storage:       collectStorageStatus(filepolicy.StorageRoot),
			Extractor:     systemExtractorStatus{Enabled: extractorClient != nil && extractorClient.Enabled()},
		}

		var wait sync.WaitGroup
		wait.Add(2)
		go func() {
			defer wait.Done()
			response.SchemaVersion, response.Database = probeDatabase(database)
		}()
		go func() {
			defer wait.Done()
			response.Extractor = probeExtractor(extractorClient)
		}()
		wait.Wait()

		c.JSON(http.StatusOK, response)
	}
}

func collectRuntimeStatus() systemRuntimeStatus {
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	used, limit := readCgroupMemory()
	return systemRuntimeStatus{
		GoVersion:                 runtime.Version(),
		CPUCount:                  runtime.NumCPU(),
		Goroutines:                runtime.NumGoroutine(),
		HeapAllocBytes:            memory.HeapAlloc,
		ContainerMemoryUsedBytes:  used,
		ContainerMemoryLimitBytes: limit,
	}
}

func collectStorageStatus(root string) systemStorageStatus {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(root, &stat); err != nil {
		return systemStorageStatus{}
	}
	blockSize := statfsFragmentSize(stat)
	return systemStorageStatus{
		TotalBytes: stat.Blocks * blockSize,
		FreeBytes:  stat.Bavail * blockSize,
	}
}

func probeDatabase(database *sql.DB) (string, systemDatabaseStatus) {
	if database == nil {
		return "", systemDatabaseStatus{}
	}
	stats := database.Stats()
	status := systemDatabaseStatus{
		OpenConnections:  stats.OpenConnections,
		InUseConnections: stats.InUse,
		IdleConnections:  stats.Idle,
	}
	ctx, cancel := context.WithTimeout(context.Background(), statusProbeTimeout)
	defer cancel()
	started := time.Now()
	if err := database.PingContext(ctx); err != nil {
		status.LatencyMS = time.Since(started).Milliseconds()
		return "", status
	}
	var version string
	if err := database.QueryRowContext(ctx, `SELECT version FROM schema_migrations ORDER BY version DESC LIMIT 1`).Scan(&version); err != nil {
		status.LatencyMS = time.Since(started).Milliseconds()
		return "", status
	}
	status.OK = true
	status.LatencyMS = time.Since(started).Milliseconds()
	return version, status
}

func probeExtractor(client *extractor.SidecarClient) systemExtractorStatus {
	status := systemExtractorStatus{Enabled: client != nil && client.Enabled()}
	if !status.Enabled {
		return status
	}
	ctx, cancel := context.WithTimeout(context.Background(), statusProbeTimeout)
	defer cancel()
	started := time.Now()
	status.OK = client.Health(ctx) == nil
	status.LatencyMS = time.Since(started).Milliseconds()
	return status
}

func readCgroupMemory() (*uint64, *uint64) {
	if runtime.GOOS != "linux" {
		return nil, nil
	}
	used := readUintFile("/sys/fs/cgroup/memory.current")
	limit := readUintFile("/sys/fs/cgroup/memory.max")
	if used == nil {
		used = readUintFile("/sys/fs/cgroup/memory/memory.usage_in_bytes")
	}
	if limit == nil {
		limit = readUintFile("/sys/fs/cgroup/memory/memory.limit_in_bytes")
	}
	return used, limit
}

func readUintFile(path string) *uint64 {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	value := strings.TrimSpace(string(content))
	if value == "" || value == "max" {
		return nil
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return nil
	}
	return &parsed
}
