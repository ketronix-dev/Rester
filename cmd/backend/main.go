package main

import (
	"encoding/json"
	"log"
	"net/http"
	"rester/auth"
	"rester/checker"
	"rester/logger"
	"rester/ui"

	"github.com/maxence-charriere/go-app/v9/pkg/app"
)

func statusHandler(w http.ResponseWriter, r *http.Request) {
	status, err := checker.CheckSystem()
	if err != nil {
		http.Error(w, "Failed to get system status: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

func main() {
	// Initialize Database
	if err := auth.InitDB(); err != nil {
		log.Fatalf("Failed to init DB: %v", err)
	}

	// Initialize Logger
	logger.Init(auth.DB)
	checker.SetDB(auth.DB)
	logger.Info("System startup initiating...")

	// Configure the go-app handler
	handler := &app.Handler{
		Name:        "Rester",
		Description: "System Status Monitor",
		Version:     "v2",
		RawHeaders: []string{
			`<link href="https://fonts.googleapis.com/css2?family=Roboto:wght@400;500;700&display=swap" rel="stylesheet">`,
			`<link rel="stylesheet" href="https://fonts.googleapis.com/css2?family=Material+Symbols+Rounded:opsz,wght,FILL,GRAD@24,400,0,0" />`,
			`<base href="/restic/" />`,
			// Google Font for Logo (Optional)
			`<link href="https://fonts.googleapis.com/css2?family=Outfit:wght@300;400;600&display=swap" rel="stylesheet">`,
		},
		LoadingLabel: "",
		Styles: []string{
			"/web/app.css",
		},
	}

	// Register the component on the server side too for correct routing generation
	app.Route("/", &ui.Home{})
	app.Route("/login", &ui.LoginPage{})
	app.Route("/register", &ui.RegisterPage{})
	app.Route("/logs", &ui.LogsPage{})

	http.Handle("/", handler)

	// Auth Endpoints
	http.HandleFunc("/api/auth/register", auth.HandleRegister)
	http.HandleFunc("/api/auth/login", auth.HandleLogin)
	http.HandleFunc("/api/auth/me", auth.HandleMe)
	http.HandleFunc("/api/auth/logout", auth.HandleLogout)
	http.HandleFunc("/api/auth/config", auth.HandleAuthConfig)

	// Protected Endpoints
	http.HandleFunc("/api/status", auth.Protect(statusHandler))

	http.HandleFunc("/api/logs", auth.Protect(func(w http.ResponseWriter, r *http.Request) {
		limit := 100
		logs, err := logger.GetLogs(limit)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(logs)
	}))

	http.HandleFunc("/api/repo", auth.Protect(func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Query().Get("name")
		if name == "" {
			http.Error(w, "Missing name parameter", http.StatusBadRequest)
			return
		}
		stats, err := checker.GetRepoStats(name)
		if err != nil {
			logger.Error("Error getting repo stats: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(stats)
	}))

	http.HandleFunc("/api/repo/stats-progress", auth.Protect(func(w http.ResponseWriter, r *http.Request) {
		progress := checker.GetStatsProgress()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(progress)
	}))

	http.HandleFunc("/api/snapshot", auth.Protect(func(w http.ResponseWriter, r *http.Request) {
		repo := r.URL.Query().Get("repo")
		id := r.URL.Query().Get("id")
		if repo == "" || id == "" {
			http.Error(w, "Missing repo or id parameter", http.StatusBadRequest)
			return
		}
		nodes, err := checker.GetSnapshotTree(repo, id)
		if err != nil {
			logger.Error("Error getting snapshot tree: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(nodes)
	}))

	http.HandleFunc("/api/restore", auth.Protect(func(w http.ResponseWriter, r *http.Request) {
		repo := r.URL.Query().Get("repo")
		if repo == "" {
			http.Error(w, "Missing repo parameter", http.StatusBadRequest)
			return
		}
		var req checker.RestoreRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		logger.Info("Starting restore for snapshot %s", req.SnapshotID)
		if err := checker.RestoreSnapshot(req, repo); err != nil {
			logger.Error("Error restoring snapshot: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		logger.Info("Restore completed successfully for %s", req.SnapshotID)
		w.WriteHeader(http.StatusOK)
	}))

	// Check API
	http.HandleFunc("/api/check/start", auth.Protect(func(w http.ResponseWriter, r *http.Request) {
		repo := r.URL.Query().Get("repo")
		if repo == "" {
			http.Error(w, "Missing repo parameter", http.StatusBadRequest)
			return
		}
		logger.Info("Starting integrity check for repo %s", repo)
		if err := checker.StartCheck(repo); err != nil {
			logger.Warn("Failed to start check: %v", err)
			http.Error(w, err.Error(), http.StatusConflict) // 409 if already running
			return
		}
		w.WriteHeader(http.StatusOK)
	}))

	http.HandleFunc("/api/check/status", auth.Protect(func(w http.ResponseWriter, r *http.Request) {
		status := checker.GetCheckStatus()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(status)
	}))

	// Debug endpoint to see raw snapshot JSON
	http.HandleFunc("/api/debug/snapshot-raw", auth.Protect(func(w http.ResponseWriter, r *http.Request) {
		repo := r.URL.Query().Get("repo")
		id := r.URL.Query().Get("id")
		if repo == "" || id == "" {
			http.Error(w, "Missing repo or id parameter", http.StatusBadRequest)
			return
		}
		rawJSON, err := checker.GetSnapshotRawJSON(repo, id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(rawJSON)
	}))

	// Debug endpoint to see raw 'restic snapshots --json' output
	http.HandleFunc("/api/debug/snapshots-raw", auth.Protect(func(w http.ResponseWriter, r *http.Request) {
		repo := r.URL.Query().Get("repo")
		if repo == "" {
			http.Error(w, "Missing repo parameter", http.StatusBadRequest)
			return
		}
		rawJSON, err := checker.GetSnapshotsRawJSON(repo)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(rawJSON)
	}))

	logger.Info("Starting Rester on :8000...")
	if err := http.ListenAndServe(":8000", nil); err != nil {
		log.Fatal(err)
	}
}
