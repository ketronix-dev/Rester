package ui

import (
	"bytes"
	"encoding/json"
	"net/http"
	"rester/auth"

	"github.com/maxence-charriere/go-app/v9/pkg/app"
)

type LoginPage struct {
	app.Compo
	Username            string
	Password            string
	Error               string
	Loading             bool
	RegistrationAllowed bool
}

func (p *LoginPage) OnMount(ctx app.Context) {
	app.Window().Get("document").Get("body").Get("classList").Call("add", "dark-theme")
	p.checkRegistration(ctx)
}

func (p *LoginPage) checkRegistration(ctx app.Context) {
	go func() {
		resp, err := http.Get("/api/auth/config")
		if err != nil {
			return
		}
		defer resp.Body.Close()
		var config map[string]bool
		if err := json.NewDecoder(resp.Body).Decode(&config); err == nil {
			ctx.Dispatch(func(ctx app.Context) {
				p.RegistrationAllowed = config["registration_allowed"]
				p.Update()
			})
		}
	}()
}

func (p *LoginPage) login(ctx app.Context, e app.Event) {
	e.PreventDefault()
	p.Loading = true
	p.Error = ""
	p.Update()

	creds := auth.Credentials{
		Username: p.Username,
		Password: p.Password,
	}
	body, _ := json.Marshal(creds)

	go func() {
		resp, err := http.Post("/api/auth/login", "application/json", bytes.NewBuffer(body))
		ctx.Dispatch(func(ctx app.Context) {
			p.Loading = false
			if err != nil {
				p.Error = "Connection failed"
				p.Update()
				return
			}
			defer resp.Body.Close()

			if resp.StatusCode == 200 {
				ctx.Navigate("/")
			} else {
				p.Error = "Invalid credentials"
				p.Update()
			}
		})
	}()
}

func (p *LoginPage) Render() app.UI {
	return app.Div().Class("auth-container").Body(
		app.Div().Class("auth-card").Body(
			// Header
			app.Div().Class("auth-header").Body(
				app.Div().Class("auth-icon").Body(
					app.Span().Class("material-symbols-rounded").Text("shield"),
				),
				app.H1().Class("auth-title").Text("Welcome Back"),
				app.Span().Class("auth-subtitle").Text("Sign in to your Rester account"),
			),

			// Form
			app.Form().OnSubmit(p.login).Style("display", "flex").Style("flex-direction", "column").Style("gap", "20px").Body(
				// Username
				app.Div().Class("md3-field").Body(
					app.Label().Text("Username"),
					app.Input().Type("text").Required(true).Value(p.Username).OnInput(p.ValueTo(&p.Username)).AutoFocus(true),
				),
				// Password
				app.Div().Class("md3-field").Body(
					app.Label().Text("Password"),
					app.Input().Type("password").Required(true).Value(p.Password).OnInput(p.ValueTo(&p.Password)),
				),

				// Error
				app.If(p.Error != "",
					app.Div().Class("auth-error").Body(
						app.Span().Class("material-symbols-rounded").Style("font-size", "18px").Text("error"),
						app.Text(p.Error),
					),
				),

				// Submit
				app.Button().Type("submit").Class("btn-m3-primary").Disabled(p.Loading).Body(
					app.If(p.Loading,
						app.Div().Class("loader-spinner").Style("width", "20px").Style("height", "20px").Style("border-color", "var(--md-sys-color-on-primary)").Style("border-bottom-color", "transparent"),
					).Else(
						app.Text("Sign In"),
						app.Span().Class("material-symbols-rounded").Style("font-size", "18px").Text("arrow_forward"),
					),
				),
			),

			// Footer
			app.If(p.RegistrationAllowed,
				app.Div().Class("auth-footer").Body(
					app.Text("Don't have an account? "),
					app.A().Class("link-primary").Href("/register").Text("Create account"),
				),
			),
		),
	)
}

type RegisterPage struct {
	app.Compo
	Username string
	Password string
	Confirm  string
	Error    string
	Loading  bool
}

func (p *RegisterPage) register(ctx app.Context, e app.Event) {
	e.PreventDefault()
	if p.Password != p.Confirm {
		p.Error = "Passwords do not match"
		p.Update()
		return
	}
	p.Loading = true
	p.Error = ""
	p.Update()

	creds := auth.Credentials{
		Username: p.Username,
		Password: p.Password,
	}
	body, _ := json.Marshal(creds)

	go func() {
		resp, err := http.Post("/api/auth/register", "application/json", bytes.NewBuffer(body))
		ctx.Dispatch(func(ctx app.Context) {
			p.Loading = false
			if err != nil {
				p.Error = "Connection failed"
				p.Update()
				return
			}
			defer resp.Body.Close()

			if resp.StatusCode == 201 {
				ctx.Navigate("/login")
			} else {
				p.Error = "Registration failed (Username taken?)"
				p.Update()
			}
		})
	}()
}

func (p *RegisterPage) Render() app.UI {
	return app.Div().Class("auth-container").Body(
		app.Div().Class("auth-card").Body(
			// Header
			app.Div().Class("auth-header").Body(
				app.Div().Class("auth-icon").Body(
					app.Span().Class("material-symbols-rounded").Text("person_add"),
				),
				app.H1().Class("auth-title").Text("Create Account"),
				app.Span().Class("auth-subtitle").Text("Join Rester to manage your backups"),
			),

			// Form
			app.Form().OnSubmit(p.register).Style("display", "flex").Style("flex-direction", "column").Style("gap", "20px").Body(
				// Username
				app.Div().Class("md3-field").Body(
					app.Label().Text("Username"),
					app.Input().Type("text").Required(true).Value(p.Username).OnInput(p.ValueTo(&p.Username)).AutoFocus(true),
				),
				// Password
				app.Div().Class("md3-field").Body(
					app.Label().Text("Password"),
					app.Input().Type("password").Required(true).Value(p.Password).OnInput(p.ValueTo(&p.Password)),
				),
				// Confirm
				app.Div().Class("md3-field").Body(
					app.Label().Text("Confirm Password"),
					app.Input().Type("password").Required(true).Value(p.Confirm).OnInput(p.ValueTo(&p.Confirm)),
				),

				// Error
				app.If(p.Error != "",
					app.Div().Class("auth-error").Body(
						app.Span().Class("material-symbols-rounded").Style("font-size", "18px").Text("error"),
						app.Text(p.Error),
					),
				),

				// Submit
				app.Button().Type("submit").Class("btn-m3-primary").Disabled(p.Loading).Body(
					app.If(p.Loading,
						app.Div().Class("loader-spinner").Style("width", "20px").Style("height", "20px").Style("border-color", "var(--md-sys-color-on-primary)").Style("border-bottom-color", "transparent"),
					).Else(
						app.Text("Sign Up"),
						app.Span().Class("material-symbols-rounded").Style("font-size", "18px").Text("arrow_forward"),
					),
				),
			),

			// Footer
			app.Div().Class("auth-footer").Body(
				app.Text("Already have an account? "),
				app.A().Class("link-primary").Href("/login").Text("Sign In"),
			),
		),
	)
}
