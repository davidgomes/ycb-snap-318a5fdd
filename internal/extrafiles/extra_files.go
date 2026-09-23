// Package extrafiles handles extra files.
package extrafiles

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/caarlos0/log"
	"github.com/goreleaser/fileglob"
	"github.com/goreleaser/goreleaser/v2/internal/artifact"
	"github.com/goreleaser/goreleaser/v2/internal/tmpl"
	"github.com/goreleaser/goreleaser/v2/pkg/config"
	"github.com/goreleaser/goreleaser/v2/pkg/context"
)

// extraArtifactMu guards find-or-create of extra-file artifacts.
// Blob publishers run in parallel and may upload the same file.
var extraArtifactMu sync.Mutex

// Find resolves extra files globs et al into a map of names/paths or an error.
func Find(ctx *context.Context, files []config.ExtraFile) (map[string]string, error) {
	t := tmpl.New(ctx)
	result := map[string]string{}
	for _, extra := range files {
		glob, err := t.Apply(extra.Glob)
		if err != nil {
			return result, fmt.Errorf("failed to apply template to glob %q: %w", extra.Glob, err)
		}
		if glob == "" {
			log.Warn("ignoring empty glob")
			continue
		}
		files, err := fileglob.Glob(glob)
		if err != nil {
			return result, fmt.Errorf("globbing failed for pattern %s: %w", extra.Glob, err)
		}
		if len(files) > 1 && extra.NameTemplate != "" {
			return result, fmt.Errorf("failed to add extra_file: %q -> %q: glob matches multiple files", extra.Glob, extra.NameTemplate)
		}
		for _, file := range files {
			info, err := os.Stat(file)
			if err == nil && info.IsDir() {
				log.Debugf("ignoring directory %s", file)
				continue
			}
			n, err := t.Apply(extra.NameTemplate)
			if err != nil {
				return result, fmt.Errorf("failed to apply template to name %q: %w", extra.NameTemplate, err)
			}
			name := filepath.Base(file)
			if n != "" {
				name = n
			}
			if old, ok := result[name]; ok {
				log.Warnf("overriding %s with %s for name %s", old, file, name)
			}
			result[name] = file
		}
	}
	return result, nil
}

// Artifact returns the uploadable artifact for an extra file.
// The first publisher registers it. Later publishers of the same file append
// their publish attempts to that artifact instead of creating another one.
func Artifact(ctx *context.Context, name, fullpath string) *artifact.Artifact {
	extraArtifactMu.Lock()
	defer extraArtifactMu.Unlock()

	for _, art := range ctx.Artifacts.Filter(artifact.ByType(artifact.UploadableFile)).List() {
		if art.Name == name && samePath(art.Path, fullpath) {
			return art
		}
	}

	art := &artifact.Artifact{
		Name: name,
		Path: fullpath,
		Type: artifact.UploadableFile,
	}
	ctx.Artifacts.Add(art)
	return art
}

func samePath(stored, full string) bool {
	if stored == full || filepath.Clean(stored) == filepath.Clean(full) {
		return true
	}
	left, err1 := filepath.Abs(stored)
	right, err2 := filepath.Abs(full)
	return err1 == nil && err2 == nil && left == right
}
