package checker

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"rester/logger"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v3/host"
)

var (
	DB *sql.DB
)

func SetDB(db *sql.DB) {
	DB = db
}

type SystemStatus struct {
	SSHInstalled    bool     `json:"ssh_installed"`
	ResticInstalled bool     `json:"restic_installed"`
	ResticVersion   string   `json:"restic_version"`
	Uptime          uint64   `json:"uptime_seconds"`
	UptimeString    string   `json:"uptime_string"`
	Repos           []string `json:"repos"`
}

type Snapshot struct {
	ID        string    `json:"id"`
	ShortID   string    `json:"short_id"`
	Time      time.Time `json:"time"`
	TimeStr   string    `json:"time_str"`
	Size      int64     `json:"size"`
	FileCount int       `json:"file_count"`
	Tree      string    `json:"tree"`
	Paths     []string  `json:"paths"`
	Hostname  string    `json:"hostname"`
	Username  string    `json:"username"`
	Tags      []string  `json:"tags"`
	Duration  string    `json:"duration"`
}

type RepoStats struct {
	Name              string     `json:"name"`
	DiskUsage         string     `json:"disk_usage"`
	UncompressedUsage string     `json:"uncompressed_usage"`
	CompressionRatio  string     `json:"compression_ratio"`
	SpaceSaving       string     `json:"space_saving"`
	SnapshotCount     int        `json:"snapshot_count"`
	BlobCount         int        `json:"blob_count"`
	Snapshots         []Snapshot `json:"snapshots"`
}

// Restic JSON structures
type SnapshotSummary struct {
	TotalFilesProcessed int       `json:"total_files_processed"`
	TotalBytesProcessed int64     `json:"total_bytes_processed"`
	BackupStart         time.Time `json:"backup_start"`
	BackupEnd           time.Time `json:"backup_end"`
}

type ResticSnapshot struct {
	ID       string          `json:"id"`
	ShortID  string          `json:"short_id"`
	Time     time.Time       `json:"time"`
	Tree     string          `json:"tree"`
	Paths    []string        `json:"paths"`
	Hostname string          `json:"hostname"`
	Username string          `json:"username"`
	Tags     []string        `json:"tags"`
	Duration string          `json:"duration"` // New field
	Summary  SnapshotSummary `json:"summary"`
}

type ResticStats struct {
	TotalSize              int64   `json:"total_size"`
	TotalUncompressedSize  int64   `json:"total_uncompressed_size"`
	CompressionRatio       float64 `json:"compression_ratio"`
	CompressionSpaceSaving float64 `json:"compression_space_saving"`
	TotalBlobCount         int     `json:"total_blob_count"`
	SnapshotsCount         int     `json:"snapshots_count"`
}

type ResticNode struct {
	Name        string      `json:"name"`
	Type        string      `json:"type"`
	Path        string      `json:"path"`
	UID         int         `json:"uid"`
	GID         int         `json:"gid"`
	Size        int64       `json:"size"`
	Mode        os.FileMode `json:"mode"`
	Permissions string      `json:"permissions"`
	Mtime       time.Time   `json:"mtime"`
	Atime       time.Time   `json:"atime"`
	Ctime       time.Time   `json:"ctime"`
	MessageType string      `json:"message_type"` // "snapshot" or "node"
}

func CheckSystem() (SystemStatus, error) {
	status := SystemStatus{}

	// Check SSH
	_, err := exec.LookPath("ssh")
	status.SSHInstalled = err == nil

	// Check Restic
	_, err = exec.LookPath("restic")
	status.ResticInstalled = err == nil
	if status.ResticInstalled {
		cmd := exec.Command("restic", "version")
		out, err := cmd.Output()
		if err == nil {
			// Output example: "restic 0.16.0 compiled with..."
			// We just want "restic 0.16.0" (first two words)
			s := string(out)
			parts := strings.Split(s, " ")
			if len(parts) >= 2 {
				status.ResticVersion = parts[1] // Just the version number e.g "0.16.0"
			} else {
				status.ResticVersion = s
			}
		}
	}

	// Check Uptime
	uptime, err := host.Uptime()
	if err != nil {
		return status, err
	}
	status.Uptime = uptime

	// Helper to format uptime
	d := time.Duration(uptime) * time.Second
	status.UptimeString = d.String()

	// Scan Repos
	status.Repos = []string{}
	reposPath := filepath.Join(".", "repos")

	// Auto-create repos directory if it doesn't exist
	if err := os.MkdirAll(reposPath, 0755); err != nil {
		log.Printf("CheckSystem: Failed to create repos directory: %v", err)
	}

	entries, err := os.ReadDir(reposPath)
	if err == nil {
		for _, e := range entries {
			if e.IsDir() {
				status.Repos = append(status.Repos, e.Name())
			}
		}
	}

	return status, nil
}

// Caching mechanism
type CachedRepoStats struct {
	Stats     RepoStats
	Timestamp time.Time
}

// Stats Worker Structures
type SnapshotCache struct {
	SnapshotID string    `json:"snapshot_id"`
	RepoName   string    `json:"repo_name"`
	Size       int64     `json:"size"`
	FileCount  int       `json:"file_count"`
	Duration   string    `json:"duration"`
	Processed  bool      `json:"processed"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type StatsProgress struct {
	Total      int    `json:"total"`
	Processed  int    `json:"processed"`
	ErrorCount int    `json:"error_count"`
	Running    bool   `json:"running"`
	CurrentID  string `json:"current_id"`
}

var (
	repoCache  = make(map[string]CachedRepoStats)
	cacheMutex sync.Mutex
	cacheTTL   = 5 * time.Minute

	statsProgress = StatsProgress{}
	styleMutex    sync.Mutex // Reusing for progress too
)

func GetStatsProgress() StatsProgress {
	styleMutex.Lock()
	defer styleMutex.Unlock()
	return statsProgress
}

func GetRepoStats(name string) (RepoStats, error) {
	cacheMutex.Lock()
	if cached, ok := repoCache[name]; ok {
		if time.Since(cached.Timestamp) < cacheTTL {
			cacheMutex.Unlock()
			log.Printf("GetRepoStats: Returning cached stats for '%s'", name)

			// Enrich cached stats with latest DB data
			// This ensures that as background worker updates DB, we see it on next poll
			if DB != nil {
				enrichedSnaps := make([]Snapshot, len(cached.Stats.Snapshots))
				copy(enrichedSnaps, cached.Stats.Snapshots)

				for i, s := range enrichedSnaps {
					var dbSize int64
					var dbCount int
					var dbDuration sql.NullString
					var dbProcessed bool
					if err := DB.QueryRow("SELECT size, file_count, duration, processed FROM snapshot_cache WHERE snapshot_id = ?", s.ID).Scan(&dbSize, &dbCount, &dbDuration, &dbProcessed); err == nil && dbProcessed {
						enrichedSnaps[i].Size = dbSize
						enrichedSnaps[i].FileCount = dbCount
						if dbDuration.Valid && dbDuration.String != "" {
							enrichedSnaps[i].Duration = dbDuration.String
						}
					}
				}
				cached.Stats.Snapshots = enrichedSnaps
			}

			return cached.Stats, nil
		}
	}
	cacheMutex.Unlock()

	logger.Info("GetRepoStats: Starting Restic CLI scan for repo '%s'", name)
	stats := RepoStats{Name: name}
	repoPath := filepath.Join(".", "repos", name)

	password := os.Getenv("RESTIC_PASSWORD")
	if password == "" {
		password = "test"
	}

	// 1. Get Snapshots
	cmdSnap := exec.Command("restic", "-r", repoPath, "snapshots", "--json")
	cmdSnap.Env = append(os.Environ(), "RESTIC_PASSWORD="+password)

	logger.Debug("GetRepoStats: Running %v", cmdSnap.Args)
	outputSnap, err := cmdSnap.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			logger.Error("GetRepoStats: Restic stderr: %s", string(exitErr.Stderr))
		}
		logger.Error("GetRepoStats: Error running snapshots command: %v", err)
		return stats, fmt.Errorf("failed to list snapshots: %w", err)
	}

	var resticSnaps []ResticSnapshot
	if err := json.Unmarshal(outputSnap, &resticSnaps); err != nil {
		log.Printf("GetRepoStats: Error unmarshaling snapshots: %v", err)
		return stats, fmt.Errorf("failed to parse snapshots: %w", err)
	}

	stats.Snapshots = make([]Snapshot, 0, len(resticSnaps))
	for _, rs := range resticSnaps {
		s := Snapshot{
			ID:        rs.ID,
			ShortID:   rs.ShortID,
			Time:      rs.Time,
			TimeStr:   rs.Time.Format("02.01.2006 15:04"),
			Size:      0,
			FileCount: 0,
			Tree:      rs.Tree,
			Paths:     rs.Paths,
			Hostname:  rs.Hostname,
			Username:  rs.Username,
			Tags:      rs.Tags,
			Duration:  "-", // Will be populated from DB or background worker
		}

		// Enrich from DB cache (size, file count, and duration)
		if DB != nil {
			var dbSize int64
			var dbCount int
			var dbDuration sql.NullString
			var dbProcessed bool
			if err := DB.QueryRow("SELECT size, file_count, duration, processed FROM snapshot_cache WHERE snapshot_id = ?", s.ID).Scan(&dbSize, &dbCount, &dbDuration, &dbProcessed); err == nil && dbProcessed {
				s.Size = dbSize
				s.FileCount = dbCount
				if dbDuration.Valid && dbDuration.String != "" {
					s.Duration = dbDuration.String
				}
			}
		}

		stats.Snapshots = append(stats.Snapshots, s)
	}
	// Calculate global stats
	totalSize := uint64(0)
	totalFiles := uint64(0)
	for _, s := range stats.Snapshots {
		totalSize += uint64(s.Size)
		totalFiles += uint64(s.FileCount)
	}

	// 2. Set snapshot count from list (fast)
	stats.SnapshotCount = len(stats.Snapshots)
	logger.Info("GetRepoStats: Parsed %d snapshots for '%s'", stats.SnapshotCount, name)

	// 3. Try to get repo stats from existing cache (if background worker already updated them)
	cacheMutex.Lock()
	if cached, ok := repoCache[name]; ok {
		// Use cached disk usage stats if they are real values (not placeholders)
		if cached.Stats.DiskUsage != "" && cached.Stats.DiskUsage != "Calculating..." {
			stats.DiskUsage = cached.Stats.DiskUsage
			stats.UncompressedUsage = cached.Stats.UncompressedUsage
			stats.CompressionRatio = cached.Stats.CompressionRatio
			stats.SpaceSaving = cached.Stats.SpaceSaving
			stats.BlobCount = cached.Stats.BlobCount
			logger.Info("GetRepoStats: Using cached disk stats - Usage: %s, Ratio: %s", stats.DiskUsage, stats.CompressionRatio)
		}
	}
	cacheMutex.Unlock()

	// If we don't have disk usage stats, set placeholder and fetch in background
	if stats.DiskUsage == "" {
		stats.DiskUsage = "Calculating..."
		stats.UncompressedUsage = "Calculating..."
		stats.CompressionRatio = "-"
		stats.SpaceSaving = "-"
		logger.Info("GetRepoStats: No cached disk stats, will calculate in background")
	}

	// Cache the snapshot list (preserving existing disk stats if available)
	cacheMutex.Lock()
	repoCache[name] = CachedRepoStats{Stats: stats, Timestamp: time.Now()}
	cacheMutex.Unlock()

	logger.Info("GetRepoStats: Returning stats - DiskUsage: %s, Snapshots: %d, Ratio: %s, Blobs: %d",
		stats.DiskUsage, stats.SnapshotCount, stats.CompressionRatio, stats.BlobCount)

	// Trigger background processing for both snapshot details AND repo stats
	ProcessSnapshotStats(name, stats.Snapshots, repoPath, password)

	return stats, nil
}

// Tree Cache
var (
	treeCache      = make(map[string][]ResticNode)
	treeCacheMutex sync.Mutex
)

func GetSnapshotTree(repoName, snapshotID string) ([]ResticNode, error) {
	cacheKey := repoName + "-" + snapshotID
	treeCacheMutex.Lock()
	if nodes, ok := treeCache[cacheKey]; ok {
		treeCacheMutex.Unlock()
		// logger.Debug("GetSnapshotTree: Cache hit for %s", snapshotID)
		return nodes, nil
	}
	treeCacheMutex.Unlock()

	repoPath := filepath.Join(".", "repos", repoName)
	password := os.Getenv("RESTIC_PASSWORD")
	if password == "" {
		password = "test"
	}

	cmd := exec.Command("restic", "-r", repoPath, "ls", snapshotID, "--json")
	cmd.Env = append(os.Environ(), "RESTIC_PASSWORD="+password)

	start := time.Now()
	logger.Info("GetSnapshotTree: Starting ls for snapshot %s", snapshotID)

	output, err := cmd.Output()
	duration := time.Since(start)

	if err != nil {
		logger.Error("GetSnapshotTree: Failed after %v: %v", duration, err)
		return nil, err
	}
	logger.Info("GetSnapshotTree: Completed in %v, output size: %d bytes", duration, len(output))

	var nodes []ResticNode
	current := []byte(output)
	for len(current) > 0 {
		idx := -1
		for i, b := range current {
			if b == '\n' {
				idx = i
				break
			}
		}

		var line []byte
		if idx == -1 {
			line = current
			current = nil
		} else {
			line = current[:idx]
			current = current[idx+1:]
		}

		if len(line) == 0 {
			continue
		}

		var node ResticNode
		if err := json.Unmarshal(line, &node); err == nil {
			if node.MessageType == "node" {
				nodes = append(nodes, node)
			}
		}
	}

	logger.Info("GetSnapshotTree: Parsed %d nodes", len(nodes))

	treeCacheMutex.Lock()
	treeCache[cacheKey] = nodes
	treeCacheMutex.Unlock()

	return nodes, nil
}

type RestoreRequest struct {
	SnapshotID string   `json:"snapshot_id"`
	RestoreAll bool     `json:"restore_all"`
	Includes   []string `json:"includes"`
	Excludes   []string `json:"excludes"`
}

func RestoreSnapshot(req RestoreRequest, repoName string) error {
	logger.Info("RestoreSnapshot: Request for repo %s, snap %s", repoName, req.SnapshotID)

	repoPath := filepath.Join(".", "repos", repoName)
	password := os.Getenv("RESTIC_PASSWORD")
	if password == "" {
		password = "test"
	}

	// Target Directory: ./restore/<snapshot_id>
	restoreRoot := filepath.Join(".", "restore")
	targetDir := filepath.Join(restoreRoot, req.SnapshotID)

	absRestoreRoot, err := filepath.Abs(restoreRoot)
	if err != nil {
		return fmt.Errorf("failed to get absolute path for restore root: %w", err)
	}

	absTarget, err := filepath.Abs(targetDir)
	if err != nil {
		return fmt.Errorf("failed to get absolute path for restore dir: %w", err)
	}

	// Auto-Cleanup: Remove the ENTIRE ./restore root CONTENTS to ensure only the latest restore exists
	// User requested NOT to delete the root folder itself, just contents.
	logger.Info("RestoreSnapshot: Wiping contents of restore root %s", absRestoreRoot)

	dirEntries, err := os.ReadDir(absRestoreRoot)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to read restore root: %w", err)
	}

	for _, entry := range dirEntries {
		entryPath := filepath.Join(absRestoreRoot, entry.Name())
		if err := os.RemoveAll(entryPath); err != nil {
			return fmt.Errorf("failed to delete %s: %w", entry.Name(), err)
		}
	}

	if err := os.MkdirAll(absTarget, 0755); err != nil {
		return fmt.Errorf("failed to create restore dir: %w", err)
	}

	args := []string{"-r", repoPath, "restore", req.SnapshotID, "--target", absTarget}

	if !req.RestoreAll {
		for _, inc := range req.Includes {
			args = append(args, "--include", inc)
		}
		for _, exc := range req.Excludes {
			args = append(args, "--exclude", exc)
		}
	}

	cmd := exec.Command("restic", args...)
	cmd.Env = append(os.Environ(), "RESTIC_PASSWORD="+password)

	logger.Info("RestoreSnapshot: Running %v", cmd.Args)
	output, err := cmd.CombinedOutput()
	if err != nil {
		logger.Error("RestoreSnapshot: Error: %v, Output: %s", err, string(output))
		return fmt.Errorf("restore failed: %s", string(output)) // Return output as error message
	}

	logger.Info("RestoreSnapshot: Success")
	return nil
}

// CheckTask Manager
type CheckTask struct {
	sync.Mutex
	Running bool     `json:"running"`
	Repo    string   `json:"repo"`
	Logs    []string `json:"logs"`
	Error   string   `json:"error"`
	Done    bool     `json:"done"`
}

var currentCheckTask = &CheckTask{}

type CheckStatus struct {
	Running bool     `json:"running"`
	Repo    string   `json:"repo"`
	Logs    []string `json:"logs"`
	Error   string   `json:"error"`
	Done    bool     `json:"done"`
}

func StartCheck(repoName string) error {
	currentCheckTask.Lock()
	defer currentCheckTask.Unlock()

	if currentCheckTask.Running {
		return fmt.Errorf("a check is already running for repo %s", currentCheckTask.Repo)
	}

	currentCheckTask.Running = true
	currentCheckTask.Repo = repoName
	currentCheckTask.Logs = []string{} // Reset logs
	currentCheckTask.Error = ""
	currentCheckTask.Done = false

	go runCheckProcess(repoName)
	return nil
}

func StopCheck() {
	// Not implemented yet (needs context cancellation or process kill)
	// For now, client just stops polling, but backend continues.
}

func GetCheckStatus() CheckStatus {
	currentCheckTask.Lock()
	defer currentCheckTask.Unlock()

	// Return copy to avoid race
	logsCopy := make([]string, len(currentCheckTask.Logs))
	copy(logsCopy, currentCheckTask.Logs)

	return CheckStatus{
		Running: currentCheckTask.Running,
		Repo:    currentCheckTask.Repo,
		Logs:    logsCopy,
		Error:   currentCheckTask.Error,
		Done:    currentCheckTask.Done,
	}
}

func runCheckProcess(repoName string) {
	logger.Info("StartCheck: Starting integrity check for repo %s", repoName)
	repoPath := filepath.Join(".", "repos", repoName)
	password := os.Getenv("RESTIC_PASSWORD")
	if password == "" {
		password = "test"
	}

	cmd := exec.Command("restic", "-r", repoPath, "check")
	cmd.Env = append(os.Environ(), "RESTIC_PASSWORD="+password)

	// Capture stdout and stderr
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()

	if err := cmd.Start(); err != nil {
		logger.Error("StartCheck: Failed to start process: %v", err)
		currentCheckTask.Lock()
		currentCheckTask.Running = false
		currentCheckTask.Done = true
		currentCheckTask.Error = err.Error()
		currentCheckTask.Logs = append(currentCheckTask.Logs, fmt.Sprintf("Failed to start: %v", err))
		currentCheckTask.Unlock()
		return
	}

	// Stream logs
	// Helper to read and append
	readOutput := func(r io.Reader, label string) {
		buf := make([]byte, 1024)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				lines := strings.Split(string(buf[:n]), "\n")
				currentCheckTask.Lock()
				for _, line := range lines {
					if line != "" {
						currentCheckTask.Logs = append(currentCheckTask.Logs, line)
						// Optional: Log every line to DB? Might be noisy.
						// logger.Debug("Check: %s", line)
					}
				}
				currentCheckTask.Unlock()
			}
			if err != nil {
				break
			}
		}
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); readOutput(stdout, "stdout") }()
	go func() { defer wg.Done(); readOutput(stderr, "stderr") }()

	wg.Wait()
	err := cmd.Wait()

	currentCheckTask.Lock()
	currentCheckTask.Running = false
	currentCheckTask.Done = true
	if err != nil {
		currentCheckTask.Error = err.Error()
		logger.Error("StartCheck: Integrity check failed: %v", err)
		currentCheckTask.Logs = append(currentCheckTask.Logs, fmt.Sprintf("Check failed: %v", err))
	} else {
		logger.Info("StartCheck: Integrity check completed successfully")
		currentCheckTask.Logs = append(currentCheckTask.Logs, "Check completed successfully.")
	}
	currentCheckTask.Unlock()
}

// --- Background Stats Worker ---

func ProcessSnapshotStats(repoName string, snapshots []Snapshot, repoPath, password string) {
	styleMutex.Lock()
	if statsProgress.Running {
		styleMutex.Unlock()
		return // Already running
	}
	statsProgress = StatsProgress{
		Total:     len(snapshots) + 1, // +1 for repo stats
		Processed: 0,
		Running:   true,
	}
	styleMutex.Unlock()

	go func() {
		logger.Info("StatsWorker: Starting background stats enrichment for %d snapshots in %s", len(snapshots), repoName)

		// 1. First, fetch expensive repo stats in background
		cmdStats := exec.Command("restic", "-r", repoPath, "stats", "--mode", "raw-data", "--json")
		cmdStats.Env = append(os.Environ(), "RESTIC_PASSWORD="+password)
		outputStats, err := cmdStats.Output()
		if err == nil {
			var rStats ResticStats
			if json.Unmarshal(outputStats, &rStats) == nil {
				cacheMutex.Lock()
				if cached, ok := repoCache[repoName]; ok {
					cached.Stats.DiskUsage = formatBytes(rStats.TotalSize)
					cached.Stats.UncompressedUsage = formatBytes(rStats.TotalUncompressedSize)
					if rStats.TotalSize > 0 {
						ratio := float64(rStats.TotalUncompressedSize) / float64(rStats.TotalSize)
						cached.Stats.CompressionRatio = fmt.Sprintf("%.2fx", ratio)
						savingPct := (float64(rStats.TotalUncompressedSize-rStats.TotalSize) / float64(rStats.TotalUncompressedSize)) * 100
						cached.Stats.SpaceSaving = fmt.Sprintf("%.1f%%", savingPct)
					}
					cached.Stats.BlobCount = rStats.TotalBlobCount
					repoCache[repoName] = cached
					logger.Info("StatsWorker: Repo stats updated - Size: %s, Ratio: %s", cached.Stats.DiskUsage, cached.Stats.CompressionRatio)
				}
				cacheMutex.Unlock()
			}
		}
		styleMutex.Lock()
		statsProgress.Processed++
		styleMutex.Unlock()

		// 2. Process each snapshot
		for _, s := range snapshots {
			styleMutex.Lock()
			statsProgress.CurrentID = s.ShortID
			styleMutex.Unlock()

			// Check DB Cache
			var cachedSize int64
			var cachedCount int
			var cachedDuration sql.NullString
			var cachedProcessed bool
			if DB != nil {
				err := DB.QueryRow("SELECT size, file_count, duration, processed FROM snapshot_cache WHERE snapshot_id = ?", s.ID).Scan(&cachedSize, &cachedCount, &cachedDuration, &cachedProcessed)
				// Only skip if we have REAL duration (not "-" placeholder)
				if err == nil && cachedProcessed && cachedDuration.Valid && cachedDuration.String != "" && cachedDuration.String != "-" {
					// Already fully processed with valid duration, skip
					logger.Debug("StatsWorker: Skipping %s (cached: size=%d, duration=%s)", s.ShortID, cachedSize, cachedDuration.String)
					styleMutex.Lock()
					statsProgress.Processed++
					styleMutex.Unlock()
					continue
				}
				logger.Info("StatsWorker: Processing %s (cached: processed=%v, duration=%v)", s.ShortID, cachedProcessed, cachedDuration)
			}

			size := cachedSize
			count := cachedCount
			duration := ""

			// Get size/count if not already cached
			if size == 0 {
				cmd := exec.Command("restic", "-r", repoPath, "stats", s.ID, "--mode", "restore-size", "--json")
				cmd.Env = append(os.Environ(), "RESTIC_PASSWORD="+password)
				out, err := cmd.Output()

				if err == nil {
					var res struct {
						TotalSize      int64 `json:"total_size"`
						TotalFileCount int   `json:"total_file_count"`
					}
					if json.Unmarshal(out, &res) == nil {
						size = res.TotalSize
						count = res.TotalFileCount
					}
				} else {
					styleMutex.Lock()
					statsProgress.ErrorCount++
					styleMutex.Unlock()
				}
			}

			// Get duration via 'restic cat snapshot' (always fetch if not cached)
			if !cachedDuration.Valid || cachedDuration.String == "" {
				duration = getSnapshotDuration(repoPath, s.ID, password)
			} else {
				duration = cachedDuration.String
			}

			// Update Progress
			styleMutex.Lock()
			statsProgress.Processed++
			styleMutex.Unlock()

			// Save to DB
			if DB != nil {
				_, err := DB.Exec(`
					INSERT INTO snapshot_cache (snapshot_id, repo_name, size, file_count, duration, processed)
					VALUES (?, ?, ?, ?, ?, ?)
					ON DUPLICATE KEY UPDATE size=?, file_count=?, duration=?, processed=?, updated_at=CURRENT_TIMESTAMP
				`, s.ID, repoName, size, count, duration, true, size, count, duration, true)
				if err != nil {
					logger.Error("StatsWorker: Failed to save cache for %s: %v", s.ShortID, err)
				} else {
					logger.Info("StatsWorker: Processed %s -> Size: %s, Files: %d, Duration: %s", s.ShortID, formatBytes(size), count, duration)
				}
			}
		}

		styleMutex.Lock()
		statsProgress.Running = false
		statsProgress.CurrentID = ""
		styleMutex.Unlock()
		logger.Info("StatsWorker: Finished enrichment")
	}()
}

// getSnapshotDuration extracts backup duration from restic cat snapshot output
func getSnapshotDuration(repoPath, snapshotID, password string) string {
	logger.Info("getSnapshotDuration: Running 'restic cat snapshot %s'", snapshotID)
	cmd := exec.Command("restic", "-r", repoPath, "cat", "snapshot", snapshotID)
	cmd.Env = append(os.Environ(), "RESTIC_PASSWORD="+password)
	out, err := cmd.Output()
	if err != nil {
		logger.Error("getSnapshotDuration: Command failed for %s: %v", snapshotID, err)
		return "-"
	}

	logger.Debug("getSnapshotDuration: Raw output length: %d bytes", len(out))

	var snapData struct {
		Summary struct {
			BackupStart time.Time `json:"backup_start"`
			BackupEnd   time.Time `json:"backup_end"`
		} `json:"summary"`
	}

	if err := json.Unmarshal(out, &snapData); err != nil {
		logger.Error("getSnapshotDuration: JSON unmarshal failed for %s: %v", snapshotID, err)
		return "-"
	}

	logger.Info("getSnapshotDuration: Parsed - BackupStart=%v, BackupEnd=%v", snapData.Summary.BackupStart, snapData.Summary.BackupEnd)

	if snapData.Summary.BackupStart.IsZero() || snapData.Summary.BackupEnd.IsZero() {
		logger.Warn("getSnapshotDuration: No summary data found for %s (old restic version?)", snapshotID)
		return "-"
	}

	dur := snapData.Summary.BackupEnd.Sub(snapData.Summary.BackupStart)
	result := dur.Round(time.Second).String()
	logger.Info("getSnapshotDuration: Success! Duration for %s = %s", snapshotID, result)
	return result
}

func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}
