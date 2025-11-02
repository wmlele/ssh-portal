package app

import (
	"context"
)

type Module interface {
	Start(context.Context) error
}

type App struct {
	modules []Module
}

type Option func(*App)

func WithModules(ms ...Module) Option {
	return func(a *App) { a.modules = append(a.modules, ms...) }
}

func New(opts ...Option) *App {
	a := &App{}
	for _, o := range opts {
		o(a)
	}
	return a
}

func (a *App) Run(ctx context.Context) error {
	for _, m := range a.modules {
		if err := m.Start(ctx); err != nil {
			return err
		}
	}
	return nil
}
