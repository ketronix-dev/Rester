package ui

import (
	"github.com/maxence-charriere/go-app/v9/pkg/app"
)

type StatCard struct {
	app.Compo
	Title    string
	Value    string
	SubKey   string
	SubValue string
	Icon     string
	IsGood   bool
}

func (c *StatCard) Render() app.UI {
	subTextClass := "stat-sub"
	if c.IsGood {
		// Just using inline style for simplicity as per mockup "style='color: success'"
		// But better to use a utility class if we had one for success color text specifically in sub.
		// For now, let's just append a style or class if needed.
	}

	return app.Div().Class("stat-card").Body(
		app.Div().Class("stat-card-icon").Body(
			app.Span().Class("material-symbols-rounded").Text(c.Icon),
		),
		app.Div().Class("stat-label").Text(c.Title),
		app.Div().Class("stat-value").Text(c.Value),
		app.Div().Class(subTextClass).
			Style("color", func() string {
				if c.IsGood {
					return "var(--md-sys-color-success)"
				}
				return ""
			}()).
			Text(c.SubKey+" "+c.SubValue),
	)
}
