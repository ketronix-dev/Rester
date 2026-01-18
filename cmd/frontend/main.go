package main

import (
	"rester/ui"

	"github.com/maxence-charriere/go-app/v9/pkg/app"
)

func main() {
	app.Route("/", &ui.Home{})
	app.Route("/login", &ui.LoginPage{})
	app.Route("/register", &ui.RegisterPage{})

	app.RunWhenOnBrowser()
}
