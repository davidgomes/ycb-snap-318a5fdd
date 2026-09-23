package snapshot

import "context"

type coordinatorKey struct{}

// WithCoordinator returns a context that carries c.
func WithCoordinator(ctx context.Context, c *Coordinator) context.Context {
	return context.WithValue(ctx, coordinatorKey{}, c)
}

// GetCoordinator returns the coordinator stored by WithCoordinator, or nil if
// ctx does not carry one.
func GetCoordinator(ctx context.Context) *Coordinator {
	c, _ := ctx.Value(coordinatorKey{}).(*Coordinator)
	return c
}
