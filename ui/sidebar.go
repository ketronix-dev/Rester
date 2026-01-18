package ui

import (
	"github.com/maxence-charriere/go-app/v9/pkg/app"
)

type Sidebar struct {
	app.Compo
	Uptime          string
	ResticInstalled bool
	ResticVersion   string
	Repos           []string
	SelectedRepo    string
	Theme           string
	IsOpen          bool
	OnSelect        func(app.Context, string)
	OnToggleTheme   func(app.Context, app.Event)
	OnLogout        func(app.Context, app.Event)
}

func (s *Sidebar) Render() app.UI {
	resticText := "Not Found"
	resticColor := "var(--md-sys-color-error)"
	if s.ResticInstalled {
		resticText = "Installed"
		if s.ResticVersion != "" {
			resticText = "v" + s.ResticVersion
		}
		resticColor = "var(--md-sys-color-success)"
	}

	themeIcon := "light_mode"
	if s.Theme == "dark" {
		themeIcon = "dark_mode"
	}

	sidebarClass := "sidebar"
	if s.IsOpen {
		sidebarClass += " open"
	}

	return app.Aside().Class(sidebarClass).Body(
		// Header
		app.Div().Class("sidebar-header").Body(
			app.Div().Class("brand").Body(
				app.Text("Rester"),
			),
			app.Button().Class("btn-icon").Title("Toggle Theme").OnClick(s.OnToggleTheme).Body(
				app.Span().Class("material-symbols-rounded").Text(themeIcon),
			),
		),

		// Repo List
		app.Div().Class("repo-list-container").Body(
			app.Div().Class("section-label").Text("Repositories"),
			app.Ul().Class("repo-list").Body(
				app.Range(s.Repos).Slice(func(i int) app.UI {
					repoName := s.Repos[i]
					activeClass := ""
					if s.SelectedRepo == repoName {
						activeClass = "active"
					}

					return app.Li().Class("repo-item "+activeClass).
						OnClick(func(ctx app.Context, e app.Event) {
							if s.OnSelect != nil {
								s.OnSelect(ctx, repoName)
							}
						}).
						Body(
							app.Span().Class("material-symbols-rounded").Text("folder"),
							app.Span().Class("path").Text(repoName),
						)
				}),
				// Fallback if no repos
				app.If(len(s.Repos) == 0,
					app.Li().Class("repo-item").Style("opacity", "0.5").Body(
						app.Span().Class("material-symbols-rounded").Text("info"),
						app.Span().Class("path").Text("No repos found"),
					),
				),
			),
		),

		// Footer
		app.Div().Class("sidebar-footer").Body(
			app.Div().Class("sys-stat").Body(
				app.Div().Class("sys-stat-label").Body(
					app.Span().Class("material-symbols-rounded").Text("dns"),
					app.Text("Uptime"),
				),
				app.Div().Style("font-weight", "500").Text(s.Uptime),
			),
			app.Div().Class("sys-stat").Body(
				app.Div().Class("sys-stat-label").Body(
					app.Span().Class("material-symbols-rounded").Text("deployed_code"),
					app.Text("Restic"),
				),
				app.Div().Style("display", "flex").Style("align-items", "center").Style("gap", "6px").Body(
					app.Div().Class("status-dot").Style("background-color", resticColor),
					app.Span().Text(resticText),
				),
			),
			// Logout
			app.Div().Class("sys-stat").Style("cursor", "pointer").OnClick(func(ctx app.Context, e app.Event) {
				if s.OnLogout != nil {
					e.PreventDefault()
					s.OnLogout(ctx, e)
				}
			}).Body(
				app.Div().Class("sys-stat-label").Style("color", "var(--md-sys-color-error)").Body(
					app.Span().Class("material-symbols-rounded").Text("logout"),
					app.Text("Sign Out"),
				),
			),
		),
	)
}
