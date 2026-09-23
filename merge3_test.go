package git

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMergeTextNonOverlapping(t *testing.T) {
	t.Parallel()
	got, conflict := mergeText("1\n2\n3\n", "1\nO\n3\n", "1\n2\n3\nT\n", "LABEL")
	require.False(t, conflict)
	require.Equal(t, "1\nO\n3\nT\n", got)
}

func TestMergeTextRepeatedLines(t *testing.T) {
	t.Parallel()
	got, conflict := mergeText("a\na\na\n", "a\nb\na\n", "a\nc\na\n", "LABEL")
	require.True(t, conflict)
	require.Equal(t, "a\n<<<<<<< HEAD\nb\n=======\nc\n>>>>>>> LABEL\na\n", got)
}

func TestMergeTextIdenticalEdit(t *testing.T) {
	t.Parallel()
	got, conflict := mergeText("a\n", "b\n", "b\n", "LABEL")
	require.False(t, conflict)
	require.Equal(t, "b\n", got)
}

func TestMergeTextInsertVersusModify(t *testing.T) {
	t.Parallel()
	got, conflict := mergeText("a\nb\n", "a\nX\nb\n", "a\nB\n", "LABEL")
	require.True(t, conflict)
	require.Equal(t, "a\n<<<<<<< HEAD\nX\nb\n=======\nB\n>>>>>>> LABEL\n", got)
}

func TestMergeTextAddAddCommonPrefix(t *testing.T) {
	t.Parallel()
	got, conflict := mergeText("", "start\nours\n", "start\ntheirs\n", "LABEL")
	require.True(t, conflict)
	require.Equal(t, "start\n<<<<<<< HEAD\nours\n=======\ntheirs\n>>>>>>> LABEL\n", got)
}

func TestMergeTextSamePointInsert(t *testing.T) {
	t.Parallel()
	got, conflict := mergeText("a\nb\n", "a\nX\nb\n", "a\nY\nb\n", "LABEL")
	require.True(t, conflict)
	require.Equal(t, "a\n<<<<<<< HEAD\nX\n=======\nY\n>>>>>>> LABEL\nb\n", got)
}

func TestMergeTextAdjacentLines(t *testing.T) {
	t.Parallel()
	got, conflict := mergeText("a\nb\nc\n", "A\nb\nc\n", "a\nB\nc\n", "LABEL")
	require.True(t, conflict)
	require.Equal(t, "<<<<<<< HEAD\nA\nb\n=======\na\nB\n>>>>>>> LABEL\nc\n", got)
}

func TestMergeTextSeparatedLines(t *testing.T) {
	t.Parallel()
	got, conflict := mergeText("a\nb\nc\n", "A\nb\nc\n", "a\nb\nC\n", "LABEL")
	require.False(t, conflict)
	require.Equal(t, "A\nb\nC\n", got)
}

func TestMergeTextInsertBesideChange(t *testing.T) {
	t.Parallel()
	got, conflict := mergeText("a\nb\n", "a\nX\nb\n", "A\nb\n", "LABEL")
	require.True(t, conflict)
	require.Equal(t, "<<<<<<< HEAD\na\nX\n=======\nA\n>>>>>>> LABEL\nb\n", got)
}

func TestMergeTextRepeatedLineEnds(t *testing.T) {
	t.Parallel()
	got, conflict := mergeText("a\na\n", "a\na\nb\n", "b\na\na\n", "LABEL")
	require.False(t, conflict)
	require.Equal(t, "b\na\na\nb\n", got)
}

func TestMergeTextNoTrailingNewline(t *testing.T) {
	t.Parallel()
	got, conflict := mergeText("a\nb", "a\nO", "a\nb\nT", "LABEL")
	require.True(t, conflict)
	require.Equal(t, "a\n<<<<<<< HEAD\nO\n=======\nb\nT\n>>>>>>> LABEL\n", got)
}
