// Copyright 2026 The LevelDB-Go and Pebble Authors. All rights reserved. Use
// of this source code is governed by a BSD-style license that can be found in
// the LICENSE file.

package pebble

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/cockroachdb/pebble/vfs"
	"github.com/stretchr/testify/require"
)

func TestBatchDurable(t *testing.T) {
	var mu sync.Mutex
	var infos []BatchDurableInfo
	d, err := Open("", &Options{
		FS: vfs.NewMem(),
		EventListener: &EventListener{BatchDurable: func(info BatchDurableInfo) {
			mu.Lock()
			defer mu.Unlock()
			infos = append(infos, info)
		}},
	})
	require.NoError(t, err)

	require.Equal(t, DurabilityStats{}, d.DurabilityStats())
	require.NoError(t, d.Set([]byte("a"), []byte("1"), NoSync))
	require.Len(t, infos, 0)

	b := d.NewBatch()
	require.NoError(t, b.Set([]byte("b"), []byte("2"), nil))
	require.NoError(t, b.Set([]byte("c"), []byte("3"), nil))
	require.NoError(t, d.Apply(b, &WriteOptions{Sync: true, CommitCorrelationID: 42}))
	require.Len(t, infos, 1)
	info := infos[0]
	require.NoError(t, info.Err)
	require.Equal(t, uint64(42), info.CorrelationID)
	require.Equal(t, uint32(2), info.KeyCount)
	require.Positive(t, info.ApplyDuration)
	require.Positive(t, info.SyncDuration)
	require.Equal(t, b.SeqNum(), info.SeqNum)

	b2 := d.NewBatch()
	require.NoError(t, b2.Set([]byte("d"), []byte("4"), nil))
	require.NoError(t, d.ApplyNoSyncWait(b2, Sync))
	require.NoError(t, b2.SyncWait())
	require.Len(t, infos, 2)

	require.NoError(t, d.WaitForDurability(info.SeqNum+1))
	require.NoError(t, d.WaitForDurabilityBatch([]SeqNum{0, info.SeqNum}))
	require.NoError(t, d.WaitForDurabilityBatch(nil))
	require.NoError(t, d.WaitForJobDurability(info.JobID))
	require.True(t, strings.Contains(d.WaitForJobDurability(0).Error(), "unknown"))
	require.True(t, strings.Contains(d.WaitForJobDurability(1000).Error(), "unknown"))
	require.NoError(t, <-d.DurabilityNotify(info.SeqNum))

	s := d.DurabilityStats()
	require.Equal(t, uint64(2), s.TotalDurableCommits)
	require.Equal(t, b2.SeqNum(), s.HighestDurableSeqNum)
	m := d.Metrics()
	require.Equal(t, uint64(2), m.DurableCommitCount)
	require.Positive(t, m.DurableCommitDuration)

	ch := d.DurabilityNotify(s.HighestDurableSeqNum + 100)
	errCh := make(chan error, 1)
	go func() { errCh <- d.WaitForDurabilityContext(context.Background(), s.HighestDurableSeqNum+100) }()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, d.WaitForDurabilityContext(ctx, s.HighestDurableSeqNum+100), context.Canceled)
	require.NoError(t, d.Close())
	require.ErrorIs(t, <-ch, ErrClosed)
	require.ErrorIs(t, <-errCh, ErrClosed)
}
