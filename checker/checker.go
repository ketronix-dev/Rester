package checker

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v3/host"
)

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

var (
	repoCache  = make(map[string]CachedRepoStats)
	cacheMutex sync.Mutex
	cacheTTL   = 5 * time.Minute
)

func GetRepoStats(name string) (RepoStats, error) {
	cacheMutex.Lock()
	if cached, ok := repoCache[name]; ok {
		if time.Since(cached.Timestamp) < cacheTTL {
			cacheMutex.Unlock()
			log.Printf("GetRepoStats: Returning cached stats for '%s'", name)
			return cached.Stats, nil
		}
	}
	cacheMutex.Unlock()

	log.Printf("GetRepoStats: Starting Restic CLI scan for repo '%s'", name)
	stats := RepoStats{Name: name}
	repoPath := filepath.Join(".", "repos", name)

	password := os.Getenv("RESTIC_PASSWORD")
	if password == "" {
		password = "test"
	}

	// 1. Get Snapshots
	cmdSnap := exec.Command("restic", "-r", repoPath, "snapshots", "--json")
	cmdSnap.Env = append(os.Environ(), "RESTIC_PASSWORD="+password)

	log.Printf("GetRepoStats: Running %v", cmdSnap.Args)
	outputSnap, err := cmdSnap.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			log.Printf("GetRepoStats: Restic stderr: %s", string(exitErr.Stderr))
		}
		log.Printf("GetRepoStats: Error running snapshots command: %v", err)
		return stats, fmt.Errorf("failed to list snapshots: %w", err)
	}

	var resticSnaps []ResticSnapshot
	if err := json.Unmarshal(outputSnap, &resticSnaps); err != nil {
		log.Printf("GetRepoStats: Error unmarshaling snapshots: %v", err)
		return stats, fmt.Errorf("failed to parse snapshots: %w", err)
	}

	stats.Snapshots = make([]Snapshot, 0, len(resticSnaps))
	for _, rs := range resticSnaps {
		durStr := "-"
		if !rs.Summary.BackupStart.IsZero() && !rs.Summary.BackupEnd.IsZero() {
			dur := rs.Summary.BackupEnd.Sub(rs.Summary.BackupStart)
			durStr = dur.Round(time.Second).String()
		}

		s := Snapshot{
			ID:        rs.ID,
			ShortID:   rs.ShortID,
			Time:      rs.Time,
			TimeStr:   rs.Time.Format("02.01.2006 15:04"),
			Size:      rs.Summary.TotalBytesProcessed,
			FileCount: rs.Summary.TotalFilesProcessed,
			Tree:      rs.Tree,
			Paths:     rs.Paths,
			Hostname:  rs.Hostname,
			Username:  rs.Username,
			Tags:      rs.Tags,
			Duration:  durStr,
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
	// Note: Without 'restic stats', UncompressedUsage and CompressionRatio are approximations or need separate logic.
	// For now we rely on the expensive 'restic stats' command which we run NEXT.

	// 2. Run Stats (Expensive!)
	log.Printf("GetRepoStats: Running stats command...")
	// For performance, we might want to skip this if not explicitly requested, or cache it heavily.
	// Current implementation runs it always, which is the bottleneck.
	// Optimizing to parse summary from snapshots or standard `restic stats`

	cmdStats := exec.Command("restic", "-r", repoPath, "stats", "--mode", "raw-data", "--json")
	cmdStats.Env = append(os.Environ(), "RESTIC_PASSWORD="+password)
	outputStats, err := cmdStats.Output()
	if err == nil {
		var rStats ResticStats
		if err := json.Unmarshal(outputStats, &rStats); err == nil {
			stats.DiskUsage = formatBytes(rStats.TotalSize)
			stats.UncompressedUsage = formatBytes(rStats.TotalUncompressedSize)
			if rStats.TotalSize > 0 {
				ratio := float64(rStats.TotalUncompressedSize) / float64(rStats.TotalSize)
				stats.CompressionRatio = fmt.Sprintf("%.2fx", ratio)

				// User requested percentage for savings
				savingPct := (float64(rStats.TotalUncompressedSize-rStats.TotalSize) / float64(rStats.TotalUncompressedSize)) * 100
				stats.SpaceSaving = fmt.Sprintf("%.1f%%", savingPct)
			}
			stats.SnapshotCount = rStats.SnapshotsCount
			stats.BlobCount = rStats.TotalBlobCount

			log.Printf("GetRepoStats: Restic Stats - Size: %s, Uncompressed: %s, Ratio: %s", stats.DiskUsage, stats.UncompressedUsage, stats.CompressionRatio)
		}
	} else {
		stats.DiskUsage = "0 B"
		stats.UncompressedUsage = "0 B"
	}

	return stats, nil
}

func GetSnapshotTree(repoName, snapshotID string) ([]ResticNode, error) {
	repoPath := filepath.Join(".", "repos", repoName)
	password := os.Getenv("RESTIC_PASSWORD")
	if password == "" {
		password = "test"
	}

	cmd := exec.Command("restic", "-r", repoPath, "ls", snapshotID, "--json")
	cmd.Env = append(os.Environ(), "RESTIC_PASSWORD="+password)

	start := time.Now()
	log.Printf("GetSnapshotTree: Starting ls for snapshot %s", snapshotID)

	output, err := cmd.Output()
	duration := time.Since(start)

	if err != nil {
		log.Printf("GetSnapshotTree: Failed after %v: %v", duration, err)
		return nil, err
	}
	log.Printf("GetSnapshotTree: Completed in %v, output size: %d bytes", duration, len(output))

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

	return nodes, nil
}

type RestoreRequest struct {
	SnapshotID string   `json:"snapshot_id"`
	RestoreAll bool     `json:"restore_all"`
	Includes   []string `json:"includes"`
	Excludes   []string `json:"excludes"`
}

func RestoreSnapshot(req RestoreRequest, repoName string) error {
	log.Printf("RestoreSnapshot: request for repo %s, snap %s", repoName, req.SnapshotID)

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
	log.Printf("RestoreSnapshot: Wiping contents of restore root %s", absRestoreRoot)

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

	log.Printf("RestoreSnapshot: Running %v", cmd.Args)
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("RestoreSnapshot: Error: %v, Output: %s", err, string(output))
		return fmt.Errorf("restore failed: %s", string(output)) // Return output as error message
	}

	log.Printf("RestoreSnapshot: Success")
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
		currentCheckTask.Logs = append(currentCheckTask.Logs, fmt.Sprintf("Check failed: %v", err))
	} else {
		currentCheckTask.Logs = append(currentCheckTask.Logs, "Check completed successfully.")
	}
	currentCheckTask.Unlock()
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
