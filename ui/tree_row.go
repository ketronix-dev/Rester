package ui

import (
	"fmt"

	"github.com/maxence-charriere/go-app/v9/pkg/app"
)

type TreeRow struct {
	app.Compo
	Node            TreeNode
	IsSelected      bool
	IsIndeterminate bool
	IsExpanded      bool
	Depth           int

	// Callbacks
	OnToggleSelect func(ctx app.Context, path string)
	OnToggleExpand func(ctx app.Context, path string)
}

func (r *TreeRow) Render() app.UI {
	paddingInt := r.Depth * 24
	padding := fmt.Sprintf("%dpx", paddingInt)

	icon := "description"
	color := "var(--md-sys-color-on-surface-variant)"
	sizeStr := formatSize(r.Node.Size)
	isDir := r.Node.Type == "dir"
	if isDir {
		icon = "folder"
		color = "var(--md-sys-color-primary)" // Gold/Primary for folder
		sizeStr = "-"
	}

	// Checkbox
	var checkbox app.UI
	iconName := "check_box_outline_blank"
	iconColor := "var(--md-sys-color-on-surface-variant)"

	if r.IsSelected {
		iconName = "check_box"
		iconColor = "var(--md-sys-color-primary)"
	} else if r.IsIndeterminate {
		iconName = "indeterminate_check_box"
		iconColor = "var(--md-sys-color-primary)"
	}

	checkbox = app.Span().
		Class("material-symbols-rounded").
		Style("font-size", "24px").
		Style("margin-right", "8px").
		Style("cursor", "pointer").
		Style("color", iconColor).
		Style("display", "inline-flex").
		Style("align-items", "center").
		Style("justify-content", "center").
		Style("width", "32px").
		Style("height", "32px").
		Style("margin-left", "-4px").
		Text(iconName).
		OnClick(r.handleSelect)

	// Folder toggle icon
	var toggleIcon app.UI
	if isDir {
		toggleStr := "chevron_right"
		if r.IsExpanded {
			toggleStr = "expand_more"
		}
		toggleIcon = app.Span().
			Class("material-symbols-rounded").
			Style("font-size", "20px").
			Style("margin-right", "4px").
			Style("cursor", "pointer").
			Style("user-select", "none").
			Text(toggleStr).
			OnClick(r.handleExpand)
	} else {
		toggleIcon = app.Span().Style("width", "24px").Style("display", "inline-block")
	}

	return app.Div().Class("tree-row").ID(r.Node.Path).Style("cursor", "pointer").OnClick(r.handleRowClick).Body(
		app.Div().Class("tree-col-name").Body(
			app.Div().Style("width", padding).Style("flex-shrink", "0"),
			toggleIcon,
			checkbox,
			app.Span().Class("material-symbols-rounded").Style("font-size", "20px").Style("margin-right", "12px").Style("color", color).Text(icon),
			app.Span().Style("white-space", "nowrap").Style("overflow", "hidden").Style("text-overflow", "ellipsis").Text(r.Node.Name),
		),
		app.Div().Class("tree-col-size").Text(sizeStr),
		app.Div().Class("tree-col-mode").Text(r.Node.Permissions),
		app.Div().Class("tree-col-mtime").Text(r.Node.Mtime.Format("2006-01-02 15:04")),
	)
}

func (r *TreeRow) handleSelect(ctx app.Context, e app.Event) {
	e.Call("stopPropagation")
	if r.OnToggleSelect != nil {
		r.OnToggleSelect(ctx, r.Node.Path)
	}
}

func (r *TreeRow) handleExpand(ctx app.Context, e app.Event) {
	e.Call("stopPropagation")
	if r.OnToggleExpand != nil {
		r.OnToggleExpand(ctx, r.Node.Path)
	}
}

func (r *TreeRow) handleRowClick(ctx app.Context, e app.Event) {
	// If in select mode, toggle selection
	if r.OnToggleSelect != nil {
		r.OnToggleSelect(ctx, r.Node.Path)
	}
}
