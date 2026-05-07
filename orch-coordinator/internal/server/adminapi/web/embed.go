// Package web embeds the operator-UX HTML/CSS/JS via go:embed.
package web

import "embed"

//go:embed templates/*.html
//go:embed assets/*
var FS embed.FS
