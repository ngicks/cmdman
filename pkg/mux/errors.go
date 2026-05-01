package mux

import "errors"

var (
	ErrSessionNotFound = errors.New("mux: session not found")
	ErrWindowNotFound  = errors.New("mux: window not found")
	ErrPaneNotFound    = errors.New("mux: pane not found")
	ErrSessionExists   = errors.New("mux: session already exists")
)
