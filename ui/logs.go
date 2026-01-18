package ui

import (
	"encoding/json"
	"net/http"
	"rester/checker"
	"rester/logger"

	"github.com/maxence-charriere/go-app/v9/pkg/app"
)

type LogsPage struct {
	app.Compo
	Logs        []logger.LogEntry
	Status      checker.SystemStatus
	Loading     bool
	Error       string
	SidebarOpen bool // For mobile
}

func (p *LogsPage) OnMount(ctx app.Context) {
	app.Window().Get("document").Get("body").Get("classList").Call("add", "dark-theme")
	p.loadLogs(ctx)
	p.loadSystemStatus(ctx)
}

func (p *LogsPage) loadLogs(ctx app.Context) {
	p.Loading = true
	p.Update()

	go func() {
		resp, err := http.Get("/api/logs")
		if err != nil {
			ctx.Dispatch(func(ctx app.Context) {
				p.Error = "Failed to fetch logs: " + err.Error()
				p.Loading = false
				p.Update()
			})
			return
		}
		defer resp.Body.Close()

		var logs []logger.LogEntry
		if err := json.NewDecoder(resp.Body).Decode(&logs); err != nil {
			ctx.Dispatch(func(ctx app.Context) {
				p.Error = "Failed to decode logs: " + err.Error()
				p.Loading = false
				p.Update()
			})
			return
		}

		ctx.Dispatch(func(ctx app.Context) {
			p.Logs = logs
			p.Loading = false
			p.Update()
		})
	}()
}

func (p *LogsPage) loadSystemStatus(ctx app.Context) {
	go func() {
		resp, err := http.Get("/api/status")
		if err != nil {
			return // Just fail silently on status for now or log it
		}
		defer resp.Body.Close()

		var status checker.SystemStatus
		if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
			return
		}

		ctx.Dispatch(func(ctx app.Context) {
			p.Status = status
			p.Update()
		})
	}()
}

func (p *LogsPage) toggleSidebar(ctx app.Context, e app.Event) {
	p.SidebarOpen = !p.SidebarOpen
	p.Update()
}

func (p *LogsPage) closeSidebar(ctx app.Context, e app.Event) {
	p.SidebarOpen = false
	p.Update()
}

func (p *LogsPage) Render() app.UI {
	return app.Div().Class("app-layout").Body(
		&Sidebar{
			Uptime:          p.Status.UptimeString,
			ResticInstalled: p.Status.ResticInstalled,
			ResticVersion:   p.Status.ResticVersion,
			Repos:           p.Status.Repos,
			SelectedRepo:    "",     // No repo selected on logs page
			Theme:           "dark", // Force dark or fetch from local storage if needed. Assuming dark for now.
			IsOpen:          p.SidebarOpen,
			OnSelect:        func(ctx app.Context, repo string) { ctx.Navigate("/?repo=" + repo) }, // Navigate to home with repo
			OnToggleTheme:   func(ctx app.Context, e app.Event) {},                                 // No-op or implement theme toggle
			OnLogout: func(ctx app.Context, e app.Event) {
				go func() {
					http.Post("/api/auth/logout", "application/json", nil)
					ctx.Dispatch(func(ctx app.Context) {
						ctx.Navigate("/login")
					})
				}()
			},
		},
		app.Div().Class("main-content").Body(
			app.Div().Class("top-bar").Body(
				app.Div().Body(
					app.H1().Class("page-title").Text("System Logs"),
					app.Span().Class("page-subtitle").Text("Recent system activity and errors"),
				),
			),
			app.If(p.Loading,
				app.Div().Style("padding", "20px").Text("Loading logs..."),
			).ElseIf(p.Error != "",
				app.Div().Class("auth-error").Text(p.Error),
			).Else(
				app.Div().Class("repo-panel").Style("margin-top", "24px").Body(
					app.Table().Body(
						app.THead().Body(
							app.Tr().Body(
								app.Th().Style("width", "180px").Text("Time"),
								app.Th().Style("width", "80px").Text("Level"),
								app.Th().Text("Message"),
								app.Th().Style("width", "20%").Text("Details"),
							),
						),
						app.TBody().Body(
							app.Range(p.Logs).Slice(func(i int) app.UI {
								l := p.Logs[i]
								color := "var(--md-sys-color-on-surface)"
								if l.Level == "ERROR" {
									color = "var(--md-sys-color-error)"
								} else if l.Level == "WARN" {
									color = "#FBC02D"
								}

								return app.Tr().Class("table-row").Style("display", "table-row").Body( // Override grid style from app.css
									app.Td().Text(l.CreatedAt.Format("2006-01-02 15:04:05")),
									app.Td().Style("color", color).Style("font-weight", "500").Text(l.Level),
									app.Td().Text(l.Message),
									app.Td().Style("font-family", "monospace").Style("font-size", "12px").Text(l.Details),
								)
							}),
						),
					),
				),
			),
		),
	)
}
