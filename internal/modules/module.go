package modules

import "context"

type Module interface {
	Start(context.Context) error
}
