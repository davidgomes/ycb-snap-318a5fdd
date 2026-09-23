package snapshot_test

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"

	"github.com/tetratelabs/wazero/experimental/snapshot"
	"github.com/tetratelabs/wazero/internal/testing/require"
)

func TestMarshalSnapshot(t *testing.T) {
	r := newRuntime(t)
	a, b, none := instantiate(t, r, "a", 1), instantiate(t, r, "b", 2), instantiate(t, r, "none", -1)
	require.True(t, a.Memory().WriteString(1, "a"))

	c := snapshot.NewCoordinator()
	full, err := c.CaptureSnapshot(a, b, none)
	require.NoError(t, err)
	full.SetTag("name", "full")
	full.SetTag("", "empty key")
	require.True(t, b.Memory().WriteString(pageSize+1, "b"))
	inc, err := c.CaptureIncremental(full, a, b, none)
	require.NoError(t, err)
	inc.SetTag("name", "inc")

	for _, snap := range []snapshot.Snapshot{full, inc} {
		encoded, err := snapshot.MarshalSnapshot(snap)
		require.NoError(t, err)

		decoded, err := snapshot.UnmarshalSnapshot(encoded)
		require.NoError(t, err)
		require.Equal(t, snap.Data(), decoded.Data())
		require.Equal(t, snap.Version(), decoded.Version())
		require.Equal(t, snap.Tags(), decoded.Tags())
		require.Nil(t, snap.Compare(decoded))
		require.Equal(t, bytes.Join(snap.Data(), nil), gunzip(t, decoded.CompressedData()))
		require.Equal(t, snapshot.SnapshotSummary{TotalModules: 3, TotalBytes: 3 * pageSize, Version: snap.Version()}, snapshot.Summarize(decoded))

		reencoded, err := snapshot.MarshalSnapshot(decoded)
		require.NoError(t, err)
		require.Equal(t, encoded, reencoded)

		// Decoded snapshots have no module identities, so restore by position.
		fill(a, 0xee)
		fill(b, 0xee)
		require.NoError(t, c.RestoreSnapshot(decoded, a, b, none))
		require.Equal(t, snap.Data(), [][]byte{memoryOf(a), memoryOf(b), {}})
	}

	_, err = snapshot.MarshalSnapshot(nil)
	require.EqualError(t, err, "snapshot is nil")
	require.Equal(t, snapshot.CodeNilSnapshot, snapshot.ErrorCode(err))
}

func TestUnmarshalSnapshot_invalid(t *testing.T) {
	valid, err := snapshot.MarshalSnapshot(&fakeSnapshot{data: [][]byte{{1, 2, 3}, {}}, version: 300})
	require.NoError(t, err)
	decoded, err := snapshot.UnmarshalSnapshot(valid)
	require.NoError(t, err)
	require.Equal(t, [][]byte{{1, 2, 3}, {}}, decoded.Data())
	require.Equal(t, uint64(300), decoded.Version())

	header := []byte("wzsnap\x01")
	tests := []struct {
		name        string
		data        []byte
		expectedErr string
	}{
		{name: "empty", expectedErr: "invalid snapshot encoding: unrecognized header"},
		{name: "wrong magic", data: []byte("wzsnab\x01\x00\x00\x00"), expectedErr: "invalid snapshot encoding: unrecognized header"},
		{name: "wrong format version", data: []byte("wzsnap\x02\x00\x00\x00"), expectedErr: "invalid snapshot encoding: unrecognized header"},
		{name: "trailing data", data: append(bytes.Clone(valid), 0), expectedErr: "invalid snapshot encoding: trailing data"},
		{
			name:        "huge module count",
			data:        binary.AppendUvarint(append(bytes.Clone(header), 1, 0), math.MaxUint64),
			expectedErr: "invalid snapshot encoding: count exceeds remaining data",
		},
		{
			name:        "huge module length",
			data:        binary.AppendUvarint(append(bytes.Clone(header), 1, 0, 1), math.MaxUint64),
			expectedErr: "invalid snapshot encoding: length exceeds remaining data",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			snap, err := snapshot.UnmarshalSnapshot(tc.data)
			require.EqualError(t, err, tc.expectedErr)
			require.Equal(t, snapshot.CodeInvalidEncoding, snapshot.ErrorCode(err))
			require.Nil(t, snap)
		})
	}

	t.Run("truncated", func(t *testing.T) {
		for n := 0; n < len(valid); n++ {
			snap, err := snapshot.UnmarshalSnapshot(valid[:n])
			require.Equal(t, snapshot.CodeInvalidEncoding, snapshot.ErrorCode(err), "length %d", n)
			require.Nil(t, snap)
		}
	})
}
