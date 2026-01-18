package ui

import "github.com/maxence-charriere/go-app/v9/pkg/app"

type Loader struct {
	app.Compo
}

func (l *Loader) Render() app.UI {
	return app.Div().Class("loader-container").Body(
		app.Div().Class("spinner").Body(
			app.Div().Class("double-bounce1"),
			app.Div().Class("double-bounce2"),
		),
		app.Div().Class("loader-text").Text("Scanning Repository..."),
	)
}
