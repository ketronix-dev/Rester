package main

import (
	"encoding/json"
	"log"
	"net/http"
	"rester/auth"
	"rester/checker"
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

	// Configure the go-app handler
	handler := &app.Handler{
		Name:        "Rester",
		Description: "System Status Monitor",
		Version:     "v2",
		RawHeaders: []string{
			`<link href="https://fonts.googleapis.com/css2?family=Roboto:wght@400;500;700&display=swap" rel="stylesheet">`,
			`<link rel="stylesheet" href="https://fonts.googleapis.com/css2?family=Material+Symbols+Rounded:opsz,wght,FILL,GRAD@24,400,0,0" />`,
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

	http.Handle("/", handler)

	// Auth Endpoints
	http.HandleFunc("/api/auth/register", auth.HandleRegister)
	http.HandleFunc("/api/auth/login", auth.HandleLogin)
	http.HandleFunc("/api/auth/me", auth.HandleMe)
	http.HandleFunc("/api/auth/logout", auth.HandleLogout)
	http.HandleFunc("/api/auth/config", auth.HandleAuthConfig)

	// Protected Endpoints
	http.HandleFunc("/api/status", auth.Protect(statusHandler))
	http.HandleFunc("/api/repo", auth.Protect(func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Query().Get("name")
		if name == "" {
			http.Error(w, "Missing name parameter", http.StatusBadRequest)
			return
		}
		stats, err := checker.GetRepoStats(name)
		if err != nil {
			log.Printf("Error getting repo stats: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(stats)
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
			log.Printf("Error getting snapshot tree: %v", err)
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
		if err := checker.RestoreSnapshot(req, repo); err != nil {
			log.Printf("Error restoring snapshot: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))

	// Check API
	http.HandleFunc("/api/check/start", auth.Protect(func(w http.ResponseWriter, r *http.Request) {
		repo := r.URL.Query().Get("repo")
		if repo == "" {
			http.Error(w, "Missing repo parameter", http.StatusBadRequest)
			return
		}
		if err := checker.StartCheck(repo); err != nil {
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

	log.Println("Starting Rester on :8000...")
	if err := http.ListenAndServe(":8000", nil); err != nil {
		log.Fatal(err)
	}
}
