package snapshot

import "context"

type coordinatorKey struct{}

// WithCoordinator attaches c to ctx. A nil coordinator returns ctx unchanged.
func WithCoordinator(ctx context.Context, c *Coordinator) context.Context {
	if c == nil {
		return ctx
	}
	return context.WithValue(ctx, coordinatorKey{}, c)
}

// GetCoordinator returns the coordinator stored by WithCoordinator, or nil if
// ctx has none.
func GetCoordinator(ctx context.Context) *Coordinator {
	c, _ := ctx.Value(coordinatorKey{}).(*Coordinator)
	return c
}
