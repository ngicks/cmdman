// Package core is the foundation the TUI's models are built on: the backend
// contract they talk to, the messages and commands they exchange with it, the
// row/group shapes they hold, and the render toolkit they draw with.
//
// It exists so a widget can live in its own package. Widget code and the root
// model share all of the above, and the root model's own view calls into the
// shared render toolkit — so a widget package that reached back into tui for
// any of it would close an import cycle. core is the half both sides depend on;
// nothing here imports tui.
package core
