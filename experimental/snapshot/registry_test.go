package snapshot_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/tetratelabs/wazero/experimental/snapshot"
	"github.com/tetratelabs/wazero/internal/testing/require"
)

func TestRegistry(t *testing.T) {
	const name = "TestRegistry"
	c1, c2 := snapshot.NewCoordinator(), snapshot.NewCoordinator()

	got, ok := snapshot.Get(name)
	require.False(t, ok)
	require.Nil(t, got)

	snapshot.Register(name, c1)
	got, ok = snapshot.Get(name)
	require.True(t, ok)
	require.Same(t, c1, got)

	snapshot.Register(name, c2)
	got, ok = snapshot.Get(name)
	require.True(t, ok)
	require.Same(t, c2, got)

	snapshot.Unregister(name)
	_, ok = snapshot.Get(name)
	require.False(t, ok)
	snapshot.Unregister(name)
}

func TestRegistry_concurrent(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			name := fmt.Sprintf("TestRegistry_concurrent/%d", i)
			c := snapshot.NewCoordinator()
			snapshot.Register(name, c)
			if got, ok := snapshot.Get(name); !ok || got != c {
				t.Errorf("Get(%q) = %v, %v", name, got, ok)
			}
			snapshot.Unregister(name)
		}()
	}
	wg.Wait()
}

func TestContext(t *testing.T) {
	ctx := context.Background()
	require.Nil(t, snapshot.GetCoordinator(ctx))

	c := snapshot.NewCoordinator()
	require.Same(t, c, snapshot.GetCoordinator(snapshot.WithCoordinator(ctx, c)))
}
