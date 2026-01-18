package ui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"rester/checker"
	"strings"
	"time"

	"github.com/maxence-charriere/go-app/v9/pkg/app"
)

type Home struct {
	app.Compo
	Status          checker.SystemStatus
	RepoStats       checker.RepoStats
	Loading         bool
	RepoLoading     bool
	CurrentRepo     string
	ModalSnapshotID string                // For modal display (key in TreeData)
	ModalShortID    string                // For header display
	ActiveMenuID    string                // For action menu tracking
	TreeData        map[string][]TreeNode // Cache for tree data
	TreeLoading     map[string]bool       // Loading state per snap
	ticker          *time.Ticker
	stopTick        chan bool
	savedContext    app.Context     // Persistent context from OnMount
	TreeSearchQuery string          // Search filter for tree
	TreeExpanded    map[string]bool // Expanded directory paths (if path is here, its children are shown)
	TreePage        int             // Current page for tree pagination
	Theme           string          // light or dark
	SidebarOpen     bool            // Mobile sidebar state

	// Restore State
	RestoreModalOpen  bool
	RestoreSnapshotID string
	RestoreMode       string // "question", "select", "progress", "success"
	SelectionIncludes map[string]bool
	SelectionExcludes map[string]bool

	// Stats Progress
	StatsProgress StatsProgress

	// Check State
	CheckModalOpen bool
	CheckStatus    checker.CheckStatus
}

type StatsProgress struct {
	Total      int    `json:"total"`
	Processed  int    `json:"processed"`
	ErrorCount int    `json:"error_count"`
	Running    bool   `json:"running"`
	CurrentID  string `json:"current_id"`
}

type Snapshot struct {
	ID       string    `json:"id"`
	ShortID  string    `json:"short_id"`
	Time     time.Time `json:"time"`
	Tree     string    `json:"tree"`
	Paths    []string  `json:"paths"`
	Hostname string    `json:"hostname"`
	Username string    `json:"username"`
	Tags     []string  `json:"tags"`
	Duration string    `json:"duration"`
}

type TreeNode struct {
	Name        string    `json:"name"`
	Path        string    `json:"path"`
	Type        string    `json:"type"`
	Size        int64     `json:"size"`
	Permissions string    `json:"permissions"`
	Mtime       time.Time `json:"mtime"`
}

func (h *Home) OnMount(ctx app.Context) {
	h.savedContext = ctx
	h.Loading = true

	// Load Theme
	var theme string
	ctx.LocalStorage().Get("theme", &theme)
	if theme == "dark" {
		h.Theme = "dark"
		app.Window().Get("document").Get("body").Get("classList").Call("add", "dark-theme")
	} else {
		h.Theme = "light"
	}

	h.stopTick = make(chan bool)
	h.updateStatus(ctx)

	// Poll every 1 second (Faster polling for Check progress)
	h.ticker = time.NewTicker(1 * time.Second)
	go func() {
		checkTickCount := 0
		for {
			select {
			case <-h.ticker.C:
				// Global status update every 5 seconds (every 5th tick)
				checkTickCount++
				if checkTickCount >= 5 {
					ctx.Dispatch(func(ctx app.Context) {
						h.updateStatus(ctx)
					})
					checkTickCount = 0
				}

				// Check Status polling (every 1 second if modal open)
				if h.CheckModalOpen {
					ctx.Dispatch(func(ctx app.Context) {
						h.updateCheckStatus(ctx)
					})
				}

				// Stats Progress Polling
				// Always poll if there is a repo selected, or smartly poll if we know it's running.
				// For now: Poll every 2s if RepeStats.SnapshotCount > 0
				if h.CurrentRepo != "" {
					ctx.Dispatch(func(ctx app.Context) {
						h.updateStatsProgress(ctx)
					})
				}

			case <-h.stopTick:
				return
			}
		}
	}()

	// Global click listener to close menu
	app.Window().Call("addEventListener", "click", app.FuncOf(func(this app.Value, args []app.Value) interface{} {
		if h.ActiveMenuID != "" {
			ctx.Dispatch(func(ctx app.Context) {
				h.ActiveMenuID = ""
				h.Update()
			})
		}
		return nil
	}))

	// Initial Auth Check
	h.checkAuth(ctx)
}

func (h *Home) checkAuth(ctx app.Context) {
	go func() {
		resp, err := http.Get("/api/auth/me")
		if err != nil || resp.StatusCode != 200 {
			ctx.Dispatch(func(ctx app.Context) {
				ctx.Navigate("/login")
			})
			return
		}
	}()
}

func (h *Home) logout(ctx app.Context, e app.Event) {
	go func() {
		http.Post("/api/auth/logout", "application/json", nil)
		ctx.Dispatch(func(ctx app.Context) {
			ctx.Navigate("/login")
		})
	}()
}

func (h *Home) OnDismount() {
	if h.ticker != nil {
		h.ticker.Stop()
		h.stopTick <- true
	}
}

func (h *Home) toggleTheme(ctx app.Context, e app.Event) {
	if h.Theme == "dark" {
		h.Theme = "light"
		app.Window().Get("document").Get("body").Get("classList").Call("remove", "dark-theme")
		ctx.LocalStorage().Set("theme", "light")
	} else {
		h.Theme = "dark"
		app.Window().Get("document").Get("body").Get("classList").Call("add", "dark-theme")
		ctx.LocalStorage().Set("theme", "dark")
	}
	h.Update()
}

func (h *Home) toggleSidebar(ctx app.Context, e app.Event) {
	h.SidebarOpen = !h.SidebarOpen
	h.Update()
}

func (h *Home) closeSidebar(ctx app.Context, e app.Event) {
	if h.SidebarOpen {
		h.SidebarOpen = false
		h.Update()
	}
}

func (h *Home) isParentIncluded(path string) bool {
	parts := strings.Split(path, "/")
	// Check all possible parents
	// /foo/bar/baz -> check /foo/bar, /foo
	for i := len(parts) - 1; i > 1; i-- {
		parent := strings.Join(parts[:i], "/")
		if h.SelectionIncludes[parent] {
			return true
		}
	}
	return false
}

func (h *Home) hasSelectedDescendant(path string) bool {
	prefix := path + "/"
	for k := range h.SelectionIncludes {
		if strings.HasPrefix(k, prefix) {
			return true
		}
	}
	return false
}

func (h *Home) toggleSelection(path string) {
	app.Logf("DEBUG: toggleSelection called for %s", path)
	if h.SelectionIncludes == nil {
		h.SelectionIncludes = make(map[string]bool)
	}
	if h.SelectionExcludes == nil {
		h.SelectionExcludes = make(map[string]bool)
	}

	// Check if currently effectively selected
	isIncluded := h.SelectionIncludes[path] || (h.isParentIncluded(path) && !h.SelectionExcludes[path])
	app.Logf("DEBUG: Path: %s, Included: %v (Explicit: %v, Parent: %v, Excl: %v)", path, isIncluded, h.SelectionIncludes[path], h.isParentIncluded(path), h.SelectionExcludes[path])

	// If currently selected (checked) -> Uncheck it
	if isIncluded {
		// 1. Remove explicit include
		delete(h.SelectionIncludes, path)
		// 2. If it was implicitly included by parent, we must explicitly exclude it
		if h.isParentIncluded(path) {
			h.SelectionExcludes[path] = true
		}

		// 3. Clear explicit includes for all descendants (Uncheck Parent -> Uncheck Children)
		prefix := path + "/"
		for k := range h.SelectionIncludes {
			if strings.HasPrefix(k, prefix) {
				delete(h.SelectionIncludes, k)
			}
		}
	} else {
		// Currently unselected (or indeterminate) -> Check it
		// 1. Add explicit include
		h.SelectionIncludes[path] = true
		// 2. Remove explicit exclude (if any)
		delete(h.SelectionExcludes, path)

		// 3. Clear explicit includes for descendants (Parent covers them now)
		// Optimization cleanup: if /a is checked, we don't need /a/b checked
		prefix := path + "/"
		for k := range h.SelectionIncludes {
			if strings.HasPrefix(k, prefix) {
				delete(h.SelectionIncludes, k)
			}
		}
		// Also clear excludes for descendants?
		// If /a is checked, and /a/b was excluded, we probably want to keep /a/b excluded?
		// Standard behavior: Check Parent -> Check All (reset excludes).
		for k := range h.SelectionExcludes {
			if strings.HasPrefix(k, prefix) {
				delete(h.SelectionExcludes, k)
			}
		}
	}
}

func (h *Home) updateStatus(ctx app.Context) {
	go func() {
		resp, err := http.Get("/api/status")
		if err != nil {
			app.Log("Failed to fetch status:", err)
			return
		}
		defer resp.Body.Close()

		var status checker.SystemStatus
		if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
			app.Log("Failed to decode status:", err)
			return
		}

		ctx.Dispatch(func(ctx app.Context) {
			h.Status = status
			h.Loading = false
			h.Update()
		})
	}()
}

func (h *Home) updateStatsProgress(ctx app.Context) {
	go func() {
		resp, err := http.Get("/api/repo/stats-progress")
		if err != nil {
			return
		}
		defer resp.Body.Close()

		var progress StatsProgress
		if err := json.NewDecoder(resp.Body).Decode(&progress); err != nil {
			return
		}

		h.savedContext.Dispatch(func(ctx app.Context) {
			h.StatsProgress = progress
			// If we just finished (Running became false), refresh stats once to get new numbers
			// But careful with loops.
			// Let's just update UI.
			h.Update()
		})
	}()
}

func (h *Home) updateRepoStats(ctx app.Context, repo string) {
	go func() {
		resp, err := http.Get("/api/repo?name=" + repo)
		if err != nil {
			app.Log("Failed to fetch repo stats:", err)
			return
		}
		defer resp.Body.Close()

		var stats checker.RepoStats
		if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
			app.Log("Failed to decode repo stats:", err)
			return
		}

		ctx.Dispatch(func(ctx app.Context) {
			h.RepoStats = stats
			h.RepoLoading = false
			h.Update()
		})
	}()
}

func (h *Home) selectRepo(ctx app.Context, repo string) {
	if h.CurrentRepo == repo {
		return
	}
	h.CurrentRepo = repo
	h.RepoStats = checker.RepoStats{}
	h.RepoLoading = true
	h.Update()
	h.updateRepoStats(ctx, repo)
}

func (h *Home) loadTree(ctx app.Context, id string) {
	if h.TreeLoading == nil {
		h.TreeLoading = make(map[string]bool)
	}
	h.TreeLoading[id] = true
	h.TreeSearchQuery = "" // Reset search
	h.TreeExpanded = nil   // Reset expansion (collapsed by default)
	h.TreePage = 1         // Reset pagination
	h.Update()
	app.Logf("loadTree: Starting fetch for %s", id)

	go func() {
		app.Logf("loadTree: Requesting /api/snapshot?repo=%s&id=%s", h.CurrentRepo, id)
		resp, err := http.Get(fmt.Sprintf("/api/snapshot?repo=%s&id=%s", h.CurrentRepo, id))
		if err != nil {
			app.Log("loadTree: Failed to fetch tree:", err)
			h.savedContext.Dispatch(func(ctx app.Context) {
				h.TreeLoading[id] = false
				h.Update()
			})
			return
		}
		defer resp.Body.Close()
		app.Log("loadTree: Response received")

		var nodes []TreeNode
		if err := json.NewDecoder(resp.Body).Decode(&nodes); err != nil {
			app.Log("loadTree: Failed to decode tree:", err)
			h.savedContext.Dispatch(func(ctx app.Context) {
				h.TreeLoading[id] = false
				h.Update()
			})
			return
		}
		app.Logf("loadTree: Decoded %d nodes", len(nodes))

		h.savedContext.Dispatch(func(ctx app.Context) {
			app.Log("loadTree: Dispatching update")
			if h.TreeData == nil {
				h.TreeData = make(map[string][]TreeNode)
			}
			h.TreeData[id] = nodes
			h.TreeLoading[id] = false
			h.Update()
		})
	}()
}

func (h *Home) renderTreeItems(id string) app.UI {
	if h.TreeLoading != nil && h.TreeLoading[id] {
		return app.Div().Style("padding", "16px").Text("Loading files...")
	}

	nodes, ok := h.TreeData[id]
	if !ok || len(nodes) == 0 {
		return app.Div().Style("padding", "16px").Text("This snapshot is empty or data not loaded.")
	}

	// 1. Filter nodes
	var visibleNodes []TreeNode
	query := strings.ToLower(h.TreeSearchQuery)

	for _, n := range nodes {
		// Search filtering
		if query != "" && !strings.Contains(strings.ToLower(n.Path), query) {
			continue
		}

		// Expansion logic (only if not searching)
		if query == "" {
			// Path: /foo/bar/baz
			// parts: ["", "foo", "bar", "baz"]
			// depth: len(parts)-1.
			// If depth > 2 (e.g. /foo/bar), parent is /foo. Check if /foo expanded.

			// Simple logic:
			// Calculate parent path.
			// If parent path is root (empty or /), it's visible (unless we want to hide roots? No).
			// If parent path is NOT root, check if parent in TreeExpanded.

			lastSlash := strings.LastIndex(n.Path, "/")
			if lastSlash > 0 { // Not a top level /foo
				parentPath := n.Path[:lastSlash]

				// Wait, if path is /foo/bar, parent is /foo.
				// If /foo is expanded, show bar.
				// But what about /foo/bar/baz? Parent is /foo/bar.
				// We need recursive check? No, iterating sequentially.
				// If parent /foo is expanded, /foo/bar is visible.
				// If /foo/bar is expanded, /foo/bar/baz is visible.

				// Logic: Just check direct parent.
				if !h.TreeExpanded[parentPath] {
					continue
				}
			}
		}
		visibleNodes = append(visibleNodes, n)
	}

	if len(visibleNodes) == 0 {
		return app.Div().Style("padding", "16px").Text("No files found")
	}

	// 2. Pagination State
	pageSize := 15
	totalPages := (len(visibleNodes) + pageSize - 1) / pageSize
	if h.TreePage < 1 {
		h.TreePage = 1
	}
	if h.TreePage > totalPages {
		h.TreePage = totalPages
	}

	startIndex := (h.TreePage - 1) * pageSize
	endIndex := startIndex + pageSize
	if endIndex > len(visibleNodes) {
		endIndex = len(visibleNodes)
	}

	pagedNodes := visibleNodes[startIndex:endIndex]

	// 3. Render
	return app.Div().Class("tree-container").Body(
		app.Div().Class("tree-header").Body(
			app.Div().Class("tree-col-name").Text("Name"),
			app.Div().Class("tree-col-size").Text("Size"),
			app.Div().Class("tree-col-mode").Text("Mode"),
			app.Div().Class("tree-col-mtime").Text("Modified"),
		),
		app.Range(pagedNodes).Slice(func(i int) app.UI {
			n := pagedNodes[i]

			depth := strings.Count(n.Path, "/") - 1
			if depth < 0 {
				depth = 0
			}

			// Calculate state
			isChecked := false
			isIndeterminate := false

			if h.RestoreModalOpen && h.RestoreMode == "select" {
				isChecked = h.SelectionIncludes[n.Path] || (h.isParentIncluded(n.Path) && !h.SelectionExcludes[n.Path])
				isIndeterminate = !isChecked && h.hasSelectedDescendant(n.Path)
			}

			isExpanded := false
			if h.TreeExpanded != nil {
				isExpanded = h.TreeExpanded[n.Path]
			}

			return &TreeRow{
				Node:            n,
				IsSelected:      isChecked,
				IsIndeterminate: isIndeterminate,
				IsExpanded:      isExpanded,
				Depth:           depth,
				OnToggleSelect: func(ctx app.Context, path string) {
					// We can use the path passed back, or n.Path since closure should work better with struct props
					// But n.Path is still correct in loop if carefully used.
					// Actually, calling h.toggleSelection(path) is safest.
					if h.RestoreModalOpen && h.RestoreMode == "select" {
						h.toggleSelection(path)
						h.Update()
					}
				},
				OnToggleExpand: func(ctx app.Context, path string) {
					if h.TreeExpanded == nil {
						h.TreeExpanded = make(map[string]bool)
					}
					h.TreeExpanded[path] = !h.TreeExpanded[path]
					h.Update()
				},
			}
		}),

		// Pagination Controls
		app.If(totalPages > 1,
			app.Div().Style("display", "flex").Style("align-items", "center").Style("justify-content", "center").Style("padding", "16px").Style("gap", "4px").Body(
				app.Button().
					Class("btn-browse").
					Style("width", "36px").
					Style("height", "36px").
					Style("padding", "0").
					Style("display", "flex").
					Style("align-items", "center").
					Style("justify-content", "center").
					Disabled(h.TreePage <= 1).
					OnClick(func(ctx app.Context, e app.Event) {
						h.TreePage--
						h.Update()
					}).
					Body(
						app.Span().Class("material-symbols-rounded").Text("chevron_left"),
					),

				// Numbered Pages Logic
				app.Range(generatePagination(h.TreePage, totalPages)).Slice(func(i int) app.UI {
					pageNum := generatePagination(h.TreePage, totalPages)[i]
					if pageNum == -1 {
						return app.Span().
							Style("width", "36px").
							Style("text-align", "center").
							Style("color", "var(--md-sys-color-on-surface-variant)").
							Text("...")
					}

					isActive := pageNum == h.TreePage
					bgColor := "transparent"
					color := "var(--md-sys-color-on-surface)"
					border := "1px solid transparent"
					if isActive {
						bgColor = "var(--md-sys-color-primary)"
						color = "var(--md-sys-color-on-primary)"
					} else {
						// Hover effect handled by btn-browse class, but add subtle border for non-active
						border = "1px solid var(--md-sys-color-outline-variant)"
					}

					// Capture pageNum for closure
					p := pageNum

					return app.Button().
						Class("btn-browse").
						Style("width", "36px").
						Style("height", "36px").
						Style("padding", "0").
						Style("min-width", "36px").
						Style("display", "flex").
						Style("align-items", "center").
						Style("justify-content", "center").
						Style("background-color", bgColor).
						Style("color", color).
						Style("border", border).
						OnClick(func(ctx app.Context, e app.Event) {
							h.TreePage = p
							h.Update()
						}).
						Text(fmt.Sprintf("%d", p))
				}),

				app.Button().
					Class("btn-browse").
					Style("width", "36px").
					Style("height", "36px").
					Style("padding", "0").
					Style("display", "flex").
					Style("align-items", "center").
					Style("justify-content", "center").
					Disabled(h.TreePage >= totalPages).
					OnClick(func(ctx app.Context, e app.Event) {
						h.TreePage++
						h.Update()
					}).
					Body(
						app.Span().Class("material-symbols-rounded").Text("chevron_right"),
					),
			),
		),
	)
}

func (h *Home) openRestoreModal(ctx app.Context, id string) {
	h.RestoreSnapshotID = id
	h.RestoreModalOpen = true
	h.RestoreMode = "question"
	h.SelectionIncludes = make(map[string]bool)
	h.SelectionExcludes = make(map[string]bool)
	h.Update()
}

func (h *Home) closeRestoreModal(ctx app.Context, e app.Event) {
	h.RestoreModalOpen = false
	h.Update()
}

func (h *Home) restoreAll(ctx app.Context, e app.Event) {
	h.RestoreMode = "progress"
	h.Update()

	go func() {
		req := struct {
			SnapshotID string `json:"snapshot_id"`
			RestoreAll bool   `json:"restore_all"`
		}{
			SnapshotID: h.RestoreSnapshotID,
			RestoreAll: true,
		}

		err := h.sendRestoreRequest(req)
		h.savedContext.Dispatch(func(ctx app.Context) {
			if err != nil {
				app.Window().Get("alert").Invoke("Restore Failed: " + err.Error())
				h.RestoreMode = "question" // Go back
			} else {
				h.RestoreMode = "success"
			}
			h.Update()
		})
	}()
}

func (h *Home) openFileSelection(ctx app.Context, e app.Event) {
	h.RestoreMode = "select"
	// Ensure tree is loaded for this snapshot
	// If not loaded, we should load it.
	// Reuse existing load logic?
	// loadTree checks if loaded.

	// We need to trigger loadTree for h.RestoreSnapshotID
	h.loadTree(ctx, h.RestoreSnapshotID)

	h.Update()
}

func (h *Home) restoreSelected(ctx app.Context, e app.Event) {
	h.RestoreMode = "progress"
	h.Update()

	go func() {
		includes := []string{}
		for k, v := range h.SelectionIncludes {
			if v {
				includes = append(includes, k)
			}
		}
		excludes := []string{}
		for k, v := range h.SelectionExcludes {
			if v {
				excludes = append(excludes, k)
			}
		}

		req := struct {
			SnapshotID string   `json:"snapshot_id"`
			RestoreAll bool     `json:"restore_all"`
			Includes   []string `json:"includes"`
			Excludes   []string `json:"excludes"`
		}{
			SnapshotID: h.RestoreSnapshotID,
			RestoreAll: false,
			Includes:   includes,
			Excludes:   excludes,
		}

		err := h.sendRestoreRequest(req)
		h.savedContext.Dispatch(func(ctx app.Context) {
			if err != nil {
				app.Window().Get("alert").Invoke("Restore Failed: " + err.Error())
				h.RestoreMode = "select"
			} else {
				h.RestoreMode = "success"
			}
			h.Update()
		})
	}()
}

func (h *Home) sendRestoreRequest(body interface{}) error {
	data, _ := json.Marshal(body)
	resp, err := http.Post("/api/restore?repo="+h.CurrentRepo, "application/json", bytes.NewBuffer(data))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		buf := new(bytes.Buffer)
		buf.ReadFrom(resp.Body)
		return fmt.Errorf("%s", buf.String())
	}
	return nil
}

func (h *Home) startCheck(ctx app.Context, e app.Event) {
	if h.CurrentRepo == "" {
		return
	}
	h.CheckModalOpen = true
	// Optimistic reset
	h.CheckStatus = checker.CheckStatus{
		Running: true,
		Repo:    h.CurrentRepo,
		Logs:    []string{"Starting check..."},
	}
	h.Update()

	go func() {
		resp, err := http.Post("/api/check/start?repo="+h.CurrentRepo, "application/json", nil)
		if err != nil || resp.StatusCode != 200 {
			h.savedContext.Dispatch(func(ctx app.Context) {
				h.CheckStatus.Running = false
				h.CheckStatus.Error = "Failed to start check"
				h.Update()
			})
		}
	}()
}

func (h *Home) updateCheckStatus(ctx app.Context) {
	go func() {
		resp, err := http.Get("/api/check/status")
		if err != nil {
			return
		}
		defer resp.Body.Close()

		var status checker.CheckStatus
		if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
			return
		}

		ctx.Dispatch(func(ctx app.Context) {
			h.CheckStatus = status
			h.Update()
			// Auto scroll logs
			if elem := app.Window().Get("document").Call("getElementById", "check-logs"); !elem.IsNull() {
				elem.Set("scrollTop", elem.Get("scrollHeight"))
			}
		})
	}()
}

func (h *Home) closeCheckModal(ctx app.Context, e app.Event) {
	h.CheckModalOpen = false
	h.Update()
}

func (h *Home) renderCheckModal() app.UI {
	if !h.CheckModalOpen {
		return nil
	}

	return app.Div().Class("modal-overlay").OnClick(h.closeCheckModal).Body(
		app.Div().Class("modal-content").
			OnClick(func(ctx app.Context, e app.Event) { e.Call("stopPropagation") }).
			Style("width", "800px").
			Style("max-width", "95vw").
			Style("height", "80vh").
			Body(
				// Header
				app.Div().Class("modal-header").Body(
					app.H2().Style("display", "flex").Style("align-items", "center").Body(
						app.Span().Class("material-symbols-rounded").Style("margin-right", "12px").Text("verified_user"),
						app.Text("Repository Integrity Check"),
					),
					app.Button().Class("modal-close").OnClick(h.closeCheckModal).Body(
						app.Span().Class("material-symbols-rounded").Text("close"),
					),
				),
				// Body
				app.Div().Class("modal-body").Style("display", "flex").Style("flex-direction", "column").Style("padding", "0").Body(
					// Status Bar
					app.Div().Style("padding", "16px").Style("border-bottom", "1px solid var(--md-sys-color-outline-variant)").Style("display", "flex").Style("align-items", "center").Style("justify-content", "space-between").Body(
						app.Div().Body(
							app.Div().Style("font-weight", "500").Text(h.CheckStatus.Repo),
							app.If(h.CheckStatus.Running,
								app.Div().Style("display", "flex").Style("align-items", "center").Style("gap", "8px").Style("color", "var(--md-sys-color-primary)").Body(
									app.Div().Class("loader-spinner").Style("width", "16px").Style("height", "16px").Style("border-width", "2px"),
									app.Text("Running..."),
								),
							).ElseIf(h.CheckStatus.Error != "",
								app.Div().Style("color", "var(--md-sys-color-error)").Text("Failed"),
							).ElseIf(h.CheckStatus.Done,
								app.Div().Style("color", "var(--md-sys-color-success)").Text("Completed"),
							),
						),
					),
					// Logs
					app.Div().ID("check-logs").
						Style("flex", "1").
						Style("background-color", "#1e1e1e").
						Style("color", "#f0f0f0").
						Style("font-family", "monospace").
						Style("padding", "16px").
						Style("overflow-y", "auto").
						Style("white-space", "pre-wrap").
						Style("font-size", "13px").
						Body(
							app.Range(h.CheckStatus.Logs).Slice(func(i int) app.UI {
								return app.Div().Text(h.CheckStatus.Logs[i])
							}),
							app.If(len(h.CheckStatus.Logs) == 0,
								app.Div().Style("color", "#666").Text("Waiting for logs..."),
							),
						),
				),
			),
	)
}

func (h *Home) renderRestoreModal() app.UI {
	if !h.RestoreModalOpen {
		return nil
	}

	return app.Div().Class("modal-overlay").OnClick(h.closeRestoreModal).Body(
		app.Div().Class("modal-content").OnClick(func(ctx app.Context, e app.Event) {
			e.Call("stopPropagation")
		}).Style("width", "95vw").Style("height", "90vh").Style("max-width", "none").Body(
			// Header
			app.Div().Class("modal-header").Body(
				app.H2().Text("Restore Snapshot"),
				app.Span().Class("material-symbols-rounded close-btn").Text("close").OnClick(h.closeRestoreModal),
			),

			// Content based on Mode
			app.Div().Style("padding", "24px").Style("display", "flex").Style("flex-direction", "column").Style("flex", "1").Style("overflow", "hidden").Body(
				// Step 1: Choose Mode
				app.If(h.RestoreMode == "question",
					app.Div().Class("wizard-step").Style("height", "100%").Style("display", "flex").Style("flex-direction", "column").Style("justify-content", "center").Body(
						app.P().Style("margin-bottom", "32px").Style("text-align", "center").Style("font-size", "18px").Style("color", "var(--md-sys-color-on-surface-variant)").Text("How would you like to restore this snapshot?"),
						app.Div().Style("display", "grid").Style("grid-template-columns", "1fr 1fr").Style("gap", "24px").Style("max-width", "800px").Style("margin", "0 auto").Style("width", "100%").Body(

							// Card: Restore All
							app.Div().Class("wizard-card").OnClick(h.restoreAll).Body(
								app.Span().Class("material-symbols-rounded").Style("font-size", "48px").Style("color", "var(--md-sys-color-primary)").Text("restore_page"),
								app.H3().Style("margin", "0").Style("font-size", "18px").Text("Restore Everything"),
								app.P().Style("margin", "0").Style("font-size", "14px").Style("color", "var(--md-sys-color-on-surface-variant)").Text("Recover all files and folders to ./restore directory."),
							),

							// Card: Select Files
							app.Div().Class("wizard-card").OnClick(h.openFileSelection).Body(
								app.Span().Class("material-symbols-rounded").Style("font-size", "48px").Style("color", "var(--md-sys-color-secondary)").Text("check_box"),
								app.H3().Style("margin", "0").Style("font-size", "18px").Text("Select Files"),
								app.P().Style("margin", "0").Style("font-size", "14px").Style("color", "var(--md-sys-color-on-surface-variant)").Text("Manually choose specific files or folders to restore."),
							),
						),
					),
				).ElseIf(h.RestoreMode == "select",
					// Step 2: Select Files
					app.Div().Class("wizard-step").Style("display", "flex").Style("flex-direction", "column").Style("height", "100%").Body(
						app.Div().Style("display", "flex").Style("align-items", "center").Style("margin-bottom", "16px").Body(
							app.Button().Class("btn-icon").OnClick(func(ctx app.Context, e app.Event) { h.RestoreMode = "question"; h.Update() }).Body(app.Span().Class("material-symbols-rounded").Text("arrow_back")),
							app.H3().Style("margin", "0").Style("margin-left", "8px").Text("Select Files"),
						),
						app.Div().Style("flex", "1").Style("overflow-y", "auto").Style("border", "1px solid var(--md-sys-color-outline-variant)").Style("border-radius", "8px").Body(
							h.renderTreeItems(h.RestoreSnapshotID),
						),
						app.Div().Style("margin-top", "16px").Style("display", "flex").Style("justify-content", "flex-end").Body(
							app.Button().Class("fab-extended").Text("Review & Restore").OnClick(func(ctx app.Context, e app.Event) { h.RestoreMode = "confirm"; h.Update() }),
						),
					),
				).ElseIf(h.RestoreMode == "confirm",
					// Step 3: Confirmation
					app.Div().Class("wizard-step").
						Style("display", "flex").
						Style("flex-direction", "column").
						Style("align-items", "center").
						Style("justify-content", "center").
						Style("height", "100%").
						Body(

							// Main Card Container - Simplified
							app.Div().
								Style("background-color", "var(--md-sys-color-surface-container)"). // Subtle BG
								Style("border-radius", "16px").
								Style("padding", "24px").
								Style("max-width", "480px").
								Style("width", "90%"). // Responsive width
								// Style("box-shadow", "0 2px 6px rgba(0,0,0,0.05)"). // Very subtle shadow
								Body(
									// Header Icon
									app.Div().Style("text-align", "center").Style("margin-bottom", "24px").Body(
										app.Span().Class("material-symbols-rounded").
											Style("font-size", "40px").
											Style("color", "var(--md-sys-color-primary)").
											Style("background-color", "transparent"). // Remove BG bubble
											Style("padding", "0").
											Style("margin-bottom", "16px").
											Text("rule"),
										app.H3().Style("margin", "0 0 8px 0").Style("font-size", "20px").Text("Confirm Restoration"),
										app.P().Style("color", "var(--md-sys-color-on-surface-variant)").
											Style("margin", "0").
											Style("font-size", "14px").
											Text("Ready to restore your files?"),
									),

									// Details Grid - Clean
									app.Div().
										Style("display", "flex").
										Style("flex-direction", "column").
										Style("gap", "12px").
										Style("margin-bottom", "24px").
										Body(
											// Destination
											app.Div().Style("display", "flex").Style("align-items", "center").Style("justify-content", "space-between").Style("padding", "12px").Style("background-color", "var(--md-sys-color-surface)").Style("border-radius", "8px").Body(
												app.Div().Style("display", "flex").Style("align-items", "center").Body(
													app.Span().Class("material-symbols-rounded").Style("margin-right", "12px").Style("font-size", "20px").Style("color", "var(--md-sys-color-on-surface-variant)").Text("folder_open"),
													app.Span().Style("font-size", "14px").Style("color", "var(--md-sys-color-on-surface-variant)").Text("Target"),
												),
												app.Span().Style("font-weight", "500").Text("Local folder"),
											),
											// Files Count
											app.Div().Style("display", "flex").Style("align-items", "center").Style("justify-content", "space-between").Style("padding", "12px").Style("background-color", "var(--md-sys-color-surface)").Style("border-radius", "8px").Body(
												app.Div().Style("display", "flex").Style("align-items", "center").Body(
													app.Span().Class("material-symbols-rounded").Style("margin-right", "12px").Style("font-size", "20px").Style("color", "var(--md-sys-color-on-surface-variant)").Text("checklist"),
													app.Span().Style("font-size", "14px").Style("color", "var(--md-sys-color-on-surface-variant)").Text("Selected"),
												),
												app.Span().Style("font-weight", "500").Text(fmt.Sprintf("%d items", len(h.SelectionIncludes))),
											),
										),

									// Warning Note - Subtle
									app.Div().
										Style("display", "flex").
										Style("align-items", "center").
										Style("gap", "12px").
										// Style("padding", "0 12px").
										Style("margin-bottom", "24px").
										Body(
											app.Span().Class("material-symbols-rounded").Style("color", "var(--md-sys-color-error)").Style("font-size", "20px").Text("warning"),
											app.P().Style("margin", "0").Style("font-size", "12px").Style("color", "var(--md-sys-color-on-surface-variant)").
												Text("Files in the destination folder might be overwritten."),
										),

									// Action Buttons
									app.Div().Style("display", "flex").Style("gap", "12px").Body(
										app.Button().
											Class("btn-browse").
											Style("flex", "1").
											Style("justify-content", "center").
											Style("height", "40px").
											Text("Back").
											OnClick(func(ctx app.Context, e app.Event) { h.RestoreMode = "select"; h.Update() }),
										app.Button().
											Class("fab-extended").
											Style("flex", "1").
											Style("justify-content", "center").
											Style("height", "40px").
											Style("box-shadow", "none").
											Text("Start Restore").
											OnClick(h.restoreSelected),
									),
								),
						),
				).ElseIf(h.RestoreMode == "progress",
					// Step 4: Progress
					app.Div().Class("wizard-step").
						Style("display", "flex").
						Style("flex-direction", "column").
						Style("align-items", "center").
						Style("justify-content", "center").
						Style("height", "100%").
						Body(
							app.Div().ID("app-wasm-loader").Style("position", "relative").Style("margin", "24px"),
							app.H3().Text("Restoring..."),
							app.P().Style("color", "var(--md-sys-color-on-surface-variant)").Text("This might take a while depending on size."),
						),
				).ElseIf(h.RestoreMode == "success",
					// Step 5: Success
					app.Div().Class("wizard-step").
						Style("display", "flex").
						Style("flex-direction", "column").
						Style("align-items", "center").
						Style("justify-content", "center").
						Style("height", "100%").
						Body(
							app.Span().Class("material-symbols-rounded").Style("font-size", "80px").Style("color", "#4caf50").Style("margin-bottom", "24px").Text("check_circle"),
							app.H3().Style("font-size", "24px").Style("margin-bottom", "8px").Text("Restore Completed!"),
							app.P().Style("color", "var(--md-sys-color-on-surface-variant)").Style("margin-bottom", "32px").Text("Your files have been successfully restored to the ./restore directory."),
							app.Button().Class("fab-extended").Text("Close").OnClick(h.closeRestoreModal),
						),
				),
			),
		),
	)
}

func generatePagination(current, total int) []int {
	var pages []int
	if total <= 7 {
		for i := 1; i <= total; i++ {
			pages = append(pages, i)
		}
		return pages
	}

	pages = append(pages, 1)
	if current > 3 {
		pages = append(pages, -1)
	}

	start := current - 1
	if start < 2 {
		start = 2
	}
	end := current + 1
	if end > total-1 {
		end = total - 1
	}

	for i := start; i <= end; i++ {
		pages = append(pages, i)
	}

	if current < total-2 {
		pages = append(pages, -1)
	}
	pages = append(pages, total)
	return pages
}

func (h *Home) Render() app.UI {
	diskUsage := "Checking..."
	if h.RepoStats.DiskUsage != "" {
		diskUsage = h.RepoStats.DiskUsage
	}

	snapCount := "..."
	if h.RepoStats.SnapshotCount >= 0 && h.CurrentRepo != "" && h.RepoStats.Name == h.CurrentRepo {
		snapCount = fmt.Sprintf("%d", h.RepoStats.SnapshotCount)
	}

	blobCount := "..."
	if h.RepoStats.BlobCount >= 0 && h.CurrentRepo != "" && h.RepoStats.Name == h.CurrentRepo {
		blobCount = fmt.Sprintf("%d", h.RepoStats.BlobCount)
	}

	return app.Div().Class("app-layout").Body(
		&Sidebar{
			Uptime:          h.Status.UptimeString,
			ResticInstalled: h.Status.ResticInstalled,
			ResticVersion:   h.Status.ResticVersion,
			Repos:           h.Status.Repos,
			SelectedRepo:    h.CurrentRepo,
			Theme:           h.Theme,
			IsOpen:          h.SidebarOpen,
			OnSelect:        h.selectRepo,
			OnToggleTheme:   h.toggleTheme,
			OnLogout:        h.logout,
		},
		// Mobile Sidebar Overlay
		app.If(h.SidebarOpen,
			app.Div().Class("sidebar-overlay").OnClick(h.closeSidebar),
		),
		// Check Modal
		h.renderCheckModal(),
		// Restore Modal
		h.renderRestoreModal(),

		app.Main().Class("main-content").Body(
			app.If(h.CurrentRepo == "",
				app.Div().Style("display", "flex").
					Style("flex-direction", "column").
					Style("align-items", "center").
					Style("justify-content", "center").
					Style("height", "100%").
					Style("color", "var(--md-sys-color-on-surface-variant)").
					Body(
						// Mobile Menu Button for Empty State
						app.Button().
							Class("btn-icon mobile-menu-btn").
							OnClick(h.toggleSidebar).
							Style("position", "absolute").
							Style("top", "16px").
							Style("left", "16px").
							Body(
								app.Span().Class("material-symbols-rounded").Text("menu"),
							),
						app.Span().
							Class("material-symbols-rounded").
							Style("font-size", "64px").
							Style("margin-bottom", "16px").
							Text("folder_open"),
						app.H2().Text("Select Repository"),
						app.P().Text("Select a repository from the sidebar to view details."),
					),
			).Else(
				app.Header().Class("top-bar").Body(
					app.Div().Style("display", "flex").Style("align-items", "center").Style("gap", "12px").Body(
						app.Button().
							Class("btn-icon mobile-menu-btn").
							OnClick(h.toggleSidebar).
							Body(
								app.Span().Class("material-symbols-rounded").Text("menu"),
							),
						app.Div().Body(
							app.H1().Class("page-title").Text("Backups "+h.CurrentRepo),
						),
					),
				),

				app.Div().Class("stats-grid").Body(
					&StatCard{
						Title:    "Disk Usage",
						Value:    diskUsage,
						SubKey:   "Uncompressed:",
						SubValue: h.RepoStats.UncompressedUsage,
						Icon:     "hard_drive",
					},
					&StatCard{
						Title:    "Snapshots",
						Value:    snapCount,
						SubKey:   "Blobs:",
						SubValue: blobCount,
						Icon:     "history",
					},
					&StatCard{
						Title:    "Compression",
						Value:    h.RepoStats.CompressionRatio,
						SubKey:   "Saved:",
						SubValue: h.RepoStats.SpaceSaving,
						Icon:     "percent",
					},
				),

				app.Div().Class("repo-panel").Body(
					app.Div().Class("repo-toolbar").Body(
						app.Div().Style("display", "flex").Style("align-items", "center").Style("gap", "12px").Style("width", "100%").Body(
							app.Div().Style("font-weight", "500").Style("font-size", "16px").Text("Snapshot History"),

							// Spacer
							app.Div().Style("flex-grow", "1"),

							// Progress Bar (Inline)
							app.If(h.StatsProgress.Running,
								app.Div().Class("stats-progress-inline").Style("display", "flex").Style("align-items", "center").Style("gap", "8px").Style("margin-right", "12px").Body(
									app.Span().Style("font-size", "12px").Style("color", "var(--md-sys-color-on-surface-variant)").Text(fmt.Sprintf("%d/%d", h.StatsProgress.Processed, h.StatsProgress.Total)),
									app.Progress().
										Max(h.StatsProgress.Total).
										Value(h.StatsProgress.Processed).
										Style("width", "80px").
										Style("height", "4px"),
								),
							),

							app.Button().
								Class("btn-icon").
								Title("Check Integrity").
								OnClick(h.startCheck).
								Body(
									app.Span().Class("material-symbols-rounded").Style("font-size", "20px").Text("verified_user"),
								),
							app.Button().
								Class("btn-icon").
								Title("Refresh").
								OnClick(func(ctx app.Context, e app.Event) {
									h.updateRepoStats(ctx, h.CurrentRepo)
								}).
								Body(
									app.Span().Class("material-symbols-rounded").Style("font-size", "20px").Text("refresh"),
								),
						),
					),
					app.Div().Class("snapshot-list").Body(
						app.Div().Class("table-header").Body(
							app.Div().Class("header").Text("ID"),
							app.Div().Class("header").Text("Time"),
							app.Div().Class("header").Text("Hostname"),
							app.Div().Class("header").Text("Duration"),
							app.Div().Class("header").Text("Size"),
							app.Div().Class("header").Text("Files"),
							app.Div().Class("header").Style("text-align", "right").Text("Actions"),
						),
						app.Div().Class("table-body").Body(
							app.Range(h.RepoStats.Snapshots).Slice(func(i int) app.UI {
								snap := h.RepoStats.Snapshots[i]
								shortID := snap.ShortID
								snapID := snap.ID // Full ID for menu logic
								isMenuOpen := h.ActiveMenuID == snapID

								return app.Div().Class("table-row").Body(
									app.Div().Class("cell").Attr("data-label", "ID").Style("font-family", "monospace").Style("color", "var(--md-sys-color-primary)").Text(shortID),
									app.Div().Class("cell").Attr("data-label", "Time").Text(snap.TimeStr),
									app.Div().Class("cell").Attr("data-label", "Hostname").Text(snap.Hostname),
									app.Div().Class("cell").Attr("data-label", "Duration").Text(snap.Duration),
									app.Div().Class("cell").Attr("data-label", "Size").Text(formatSize(snap.Size)),
									app.Div().Class("cell").Attr("data-label", "Files").Text(fmt.Sprintf("%d", snap.FileCount)),
									app.Div().Class("cell").Style("position", "relative").Style("display", "flex").Style("justify-content", "flex-end").Body(
										app.Button().Class("btn-icon").
											Title("Actions").
											OnClick(func(ctx app.Context, e app.Event) {
												e.Call("stopPropagation")
												if h.ActiveMenuID == snapID {
													h.ActiveMenuID = ""
												} else {
													h.ActiveMenuID = snapID
												}
												h.Update()
											}).Body(
											app.Span().Class("material-symbols-rounded").Text("more_vert"),
										),
										app.If(isMenuOpen,
											app.Div().Class("action-menu").
												OnClick(func(ctx app.Context, e app.Event) {
													e.Call("stopPropagation")
												}).
												Body(
													app.Div().Class("menu-item").OnClick(func(ctx app.Context, e app.Event) {
														h.ActiveMenuID = ""
														h.openRestoreModal(ctx, snapID)
														h.Update()
													}).Body(
														app.Span().Class("material-symbols-rounded").Text("restore"),
														app.Text("Restore"),
													),
													app.Div().Class("menu-item").
														OnClick(func(ctx app.Context, e app.Event) {
															e.Call("stopPropagation")
															h.ActiveMenuID = ""
															h.ModalSnapshotID = shortID
															h.ModalShortID = shortID
															h.loadTree(ctx, shortID)
															h.Update()
														}).Body(
														app.Span().Class("material-symbols-rounded").Text("folder_open"),
														app.Text("Browse Files"),
													),
												),
										),
									),
								)
							}),
							app.If(len(h.RepoStats.Snapshots) == 0,
								app.Div().Class("snapshot-empty").Text("No snapshots found"),
							),
						),
					),
				),
			),
		),
		app.If(h.ModalSnapshotID != "",
			app.Div().Class("modal-overlay").OnClick(func(ctx app.Context, e app.Event) {
				h.ModalSnapshotID = ""
				h.Update()
			}).Body(
				app.Div().Class("modal-content").OnClick(func(ctx app.Context, e app.Event) {
					e.Call("stopPropagation")
				}).Body(
					app.Div().Class("modal-header").Body(
						app.Div().Style("display", "flex").Style("align-items", "center").Style("gap", "16px").Body(
							app.H3().Style("margin", "0").Text("Files of snapshot "+h.ModalShortID),
							app.Input().
								Class("modal-search-input").
								Type("text").
								Placeholder("Search files...").
								Value(h.TreeSearchQuery).
								OnInput(func(ctx app.Context, e app.Event) {
									h.TreeSearchQuery = ctx.JSSrc().Get("value").String()
									h.TreePage = 1
									h.Update()
								}),
						),
						app.Button().Class("modal-close").OnClick(func(ctx app.Context, e app.Event) {
							h.ModalSnapshotID = ""
							h.Update()
						}).Body(
							app.Span().Class("material-symbols-rounded").Text("close"),
						),
					),
					app.Div().Class("modal-body").Body(
						h.renderTreeItems(h.ModalSnapshotID),
					),
				),
			),
		),
	)
}

func formatSize(b int64) string {
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
