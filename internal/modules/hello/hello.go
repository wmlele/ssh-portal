package hello

import (
	"context"
	"log/slog"
)

type Hello struct {
	msg string
}

func New(msg string) *Hello { return &Hello{msg: msg} }

func (h *Hello) Start(ctx context.Context) error {
	slog.Info("hello module started", "msg", h.msg)
	return nil
}
