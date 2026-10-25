package main

import (
	"context"
	"sync"
)

// App is the object Wails binds to the page. The page can call its exported
// methods (api.go) and nothing else, and it can reach no network listener. Wails
// runs each call in its own goroutine, so the writes are serialized here.
type App struct {
	// ctx is the Wails context. It is set when the window starts, and the
	// runtime calls that show windows and dialogs need it.
	ctx context.Context
	// writeMu serializes cluster writes, so the guard checked before a write
	// cannot race another write.
	writeMu sync.Mutex
}

// NewApp returns the App the window binds.
func NewApp() *App {
	return &App{}
}

// startup keeps the Wails context. Wails calls it when the window starts.
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}
