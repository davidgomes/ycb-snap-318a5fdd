package git

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/go-git/go-billy/v6/util"

	"github.com/go-git/go-git/v6/config"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/filemode"
	"github.com/go-git/go-git/v6/plumbing/format/index"
	"github.com/go-git/go-git/v6/plumbing/object"
	"github.com/go-git/go-git/v6/utils/ioutil"
)

// mergeHeadFile is the path, on the worktree filesystem, of the plain-text
// file that records the commit being merged. It is not a reference in the
// object or reference backend.
const mergeHeadFile = ".git/MERGE_HEAD"

// Merge merges the commit identified by target into HEAD.
//
// The default, used for nil or empty MergeOptions, fast-forwards HEAD when
// target is a descendant of HEAD. Otherwise it performs a three-way merge and
// creates a merge commit. Non-overlapping changes in a file are combined.
// Files that merge cleanly are updated even when other paths conflict.
//
// On conflicts, conflicted files contain conflict markers, the index records
// stages 1, 2 and 3 for each blob that exists, target is written to
// .git/MERGE_HEAD on the worktree filesystem, and ErrMergeConflicts is
// returned. ErrUncommittedChanges is returned when the worktree is dirty.
//
// The merge commit is created even when repository user configuration is unset.
func (w *Worktree) Merge(target plumbing.Hash, opts *MergeOptions) error {
	if opts == nil {
		opts = &MergeOptions{}
	}
	// The zero Strategy is FastForwardMerge. For Worktree.Merge that value
	// means "fast-forward if possible, otherwise three-way merge".
	if opts.Strategy != FastForwardMerge {
		return ErrUnsupportedMergeStrategy
	}

	headRef, err := w.r.Head()
	if err != nil {
		return err
	}
	if _, err := w.r.CommitObject(target); err != nil {
		return err
	}
	if err := w.ensureNoUncommittedChanges(); err != nil {
		return err
	}

	headHash := headRef.Hash()
	if headHash.Equal(target) {
		return nil
	}

	shallow := w.earliestShallow()
	ff, err := isFastForward(w.r.Storer, headHash, target, shallow)
	if err != nil {
		return err
	}
	if ff {
		return w.Reset(&ResetOptions{
			Commit: target,
			Mode:   MergeReset,
		})
	}

	behind, err := isFastForward(w.r.Storer, target, headHash, shallow)
	if err != nil {
		return err
	}
	if behind {
		return nil
	}

	return w.mergeThreeWay(headHash, target)
}

func (w *Worktree) earliestShallow() *plumbing.Hash {
	shallowList, err := w.r.Storer.Shallow()
	if err != nil || len(shallowList) == 0 {
		return nil
	}
	return &shallowList[0]
}

func (w *Worktree) ensureNoUncommittedChanges() error {
	st, err := w.Status()
	if err != nil {
		return err
	}
	for _, fs := range st {
		if isUncommitted(fs.Staging) || isUncommitted(fs.Worktree) {
			return ErrUncommittedChanges
		}
	}
	return nil
}

func isUncommitted(code StatusCode) bool {
	return code != Unmodified && code != Untracked
}

func (w *Worktree) mergeThreeWay(headHash, target plumbing.Hash) error {
	headCommit, err := w.r.CommitObject(headHash)
	if err != nil {
		return err
	}
	targetCommit, err := w.r.CommitObject(target)
	if err != nil {
		return err
	}

	bases, err := headCommit.MergeBase(targetCommit)
	if err != nil {
		return err
	}

	var baseTree *object.Tree
	if len(bases) > 0 {
		baseTree, err = bases[0].Tree()
		if err != nil {
			return err
		}
	}
	oursTree, err := headCommit.Tree()
	if err != nil {
		return err
	}
	theirsTree, err := targetCommit.Tree()
	if err != nil {
		return err
	}

	baseFiles, err := indexTree(baseTree)
	if err != nil {
		return err
	}
	oursFiles, err := indexTree(oursTree)
	if err != nil {
		return err
	}
	theirsFiles, err := indexTree(theirsTree)
	if err != nil {
		return err
	}

	label := target.String()
	results, conflict, err := w.planMerge(baseFiles, oursFiles, theirsFiles, label)
	if err != nil {
		return err
	}

	idx, err := w.r.Storer.Index()
	if err != nil {
		return err
	}
	if err := w.applyMerge(idx, results); err != nil {
		return err
	}
	if err := w.r.Storer.SetIndex(idx); err != nil {
		return err
	}
	if err := w.writeMergeHead(target); err != nil {
		return err
	}
	if conflict {
		return ErrMergeConflicts
	}

	sig := w.mergeSignature()
	_, err = w.Commit(w.mergeCommitMessage(target), &CommitOptions{
		Author:            &sig,
		Committer:         &sig,
		AllowEmptyCommits: true,
	})
	return err
}

func (w *Worktree) mergeCommitMessage(target plumbing.Hash) string {
	branch := "HEAD"
	ref, err := w.r.Head()
	if err == nil && ref.Name().IsBranch() {
		branch = ref.Name().Short()
	}
	return fmt.Sprintf("Merge commit '%s' into %s\n", target.String(), branch)
}

func (w *Worktree) mergeSignature() object.Signature {
	sig := object.Signature{
		Name:  "go-git",
		Email: "go-git@localhost",
		When:  time.Now(),
	}
	cfg, err := w.r.ConfigScoped(config.SystemScope)
	if err != nil || cfg == nil {
		return sig
	}
	switch {
	case cfg.Author.Name != "" && cfg.Author.Email != "":
		sig.Name, sig.Email = cfg.Author.Name, cfg.Author.Email
	case cfg.Committer.Name != "" && cfg.Committer.Email != "":
		sig.Name, sig.Email = cfg.Committer.Name, cfg.Committer.Email
	case cfg.User.Name != "" && cfg.User.Email != "":
		sig.Name, sig.Email = cfg.User.Name, cfg.User.Email
	}
	return sig
}

type mergeAction int

const (
	mergeKeep mergeAction = iota
	mergeWrite
	mergeDelete
	mergeConflict
)

type stageBlob struct {
	stage index.Stage
	mode  filemode.FileMode
	hash  plumbing.Hash
}

type mergeResult struct {
	name         string
	action       mergeAction
	mode         filemode.FileMode
	hash         plumbing.Hash
	content      []byte
	writeContent bool
	keepWorktree bool
	rename       string
	stages       []stageBlob
}

type sideFile struct {
	mode filemode.FileMode
	hash plumbing.Hash
}

type treeFiles struct {
	files map[string]sideFile
	dirs  map[string]struct{}
}

func indexTree(t *object.Tree) (*treeFiles, error) {
	tf := &treeFiles{
		files: make(map[string]sideFile),
		dirs:  make(map[string]struct{}),
	}
	if t == nil {
		return tf, nil
	}

	walker := object.NewTreeWalker(t, true, nil)
	defer walker.Close()
	for {
		name, entry, err := walker.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return tf, nil
			}
			return nil, err
		}
		if entry.Mode == filemode.Dir {
			tf.dirs[name] = struct{}{}
			continue
		}
		tf.files[name] = sideFile{mode: entry.Mode, hash: entry.Hash}
		addParentDirs(tf, name)
	}
}

func addParentDirs(tf *treeFiles, name string) {
	dir := path.Dir(name)
	for dir != "." && dir != "/" && dir != "" {
		tf.dirs[dir] = struct{}{}
		next := path.Dir(dir)
		if next == dir {
			break
		}
		dir = next
	}
}

func (w *Worktree) planMerge(base, ours, theirs *treeFiles, label string) ([]*mergeResult, bool, error) {
	names := fileNames(base, ours, theirs)
	results := make([]*mergeResult, 0, len(names))
	conflict := false
	for _, name := range names {
		res, err := w.planPath(name, base, ours, theirs, label)
		if err != nil {
			return nil, false, err
		}
		if res == nil {
			continue
		}
		if res.action == mergeConflict {
			conflict = true
		}
		results = append(results, res)
	}
	return results, conflict, nil
}

func fileNames(trees ...*treeFiles) []string {
	seen := make(map[string]struct{})
	names := make([]string, 0)
	for _, tree := range trees {
		for name := range tree.files {
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func (w *Worktree) planPath(name string, base, ours, theirs *treeFiles, label string) (*mergeResult, error) {
	b, bOK := base.files[name]
	o, oOK := ours.files[name]
	t, tOK := theirs.files[name]
	_, oDir := ours.dirs[name]
	_, tDir := theirs.dirs[name]

	if (oOK && tDir) || (tOK && oDir) {
		return w.planFileDir(name, b, bOK, o, oOK, t, tOK, label), nil
	}

	switch {
	case oOK && tOK:
		return w.planBoth(name, b, bOK, o, t, label)
	case oOK:
		return w.planOursOnly(name, b, bOK, o), nil
	case tOK:
		return w.planTheirsOnly(name, b, bOK, t), nil
	case bOK:
		return &mergeResult{name: name, action: mergeDelete}, nil
	default:
		return nil, nil
	}
}

func (w *Worktree) planFileDir(name string, base sideFile, hasBase bool, ours sideFile, hasOurs bool, theirs sideFile, hasTheirs bool, label string) *mergeResult {
	res := &mergeResult{
		name:   name,
		action: mergeConflict,
		stages: stagesFor(base, hasBase, ours, hasOurs, theirs, hasTheirs),
	}
	if hasOurs {
		res.rename = name + "~HEAD"
		res.hash = ours.hash
		res.mode = ours.mode
	} else {
		res.rename = name + "~" + label
		res.hash = theirs.hash
		res.mode = theirs.mode
	}
	if res.mode == filemode.Submodule {
		res.keepWorktree = true
	}
	return res
}

func (w *Worktree) planOursOnly(name string, base sideFile, hasBase bool, ours sideFile) *mergeResult {
	if !hasBase {
		return keepResult(name, ours)
	}
	if base.hash == ours.hash && base.mode == ours.mode {
		return &mergeResult{name: name, action: mergeDelete}
	}
	return &mergeResult{
		name:         name,
		action:       mergeConflict,
		keepWorktree: true,
		mode:         ours.mode,
		hash:         ours.hash,
		stages:       stagesFor(base, true, ours, true, sideFile{}, false),
	}
}

func (w *Worktree) planTheirsOnly(name string, base sideFile, hasBase bool, theirs sideFile) *mergeResult {
	if !hasBase {
		return &mergeResult{name: name, action: mergeWrite, mode: theirs.mode, hash: theirs.hash}
	}
	if base.hash == theirs.hash && base.mode == theirs.mode {
		return &mergeResult{name: name, action: mergeDelete}
	}
	res := &mergeResult{
		name:   name,
		action: mergeConflict,
		mode:   theirs.mode,
		hash:   theirs.hash,
		stages: stagesFor(base, true, sideFile{}, false, theirs, true),
	}
	if theirs.mode == filemode.Submodule {
		res.keepWorktree = true
	}
	return res
}

func (w *Worktree) planBoth(name string, base sideFile, hasBase bool, ours, theirs sideFile, label string) (*mergeResult, error) {
	if ours.hash == theirs.hash && ours.mode == theirs.mode {
		return keepResult(name, ours), nil
	}
	if !hasBase {
		return w.planAddAdd(name, ours, theirs, label)
	}
	if !sameFileKind(ours.mode, theirs.mode) || ours.mode == filemode.Submodule || theirs.mode == filemode.Submodule {
		return w.planNonTextConflict(name, base, true, ours, theirs), nil
	}
	if ours.hash == theirs.hash {
		mode := pickMode(base.mode, ours.mode, theirs.mode, true)
		if mode == ours.mode {
			return keepResult(name, ours), nil
		}
		return &mergeResult{name: name, action: mergeWrite, mode: mode, hash: ours.hash}, nil
	}
	if ours.hash == base.hash {
		mode := pickMode(base.mode, ours.mode, theirs.mode, true)
		return &mergeResult{name: name, action: mergeWrite, mode: mode, hash: theirs.hash}, nil
	}
	if theirs.hash == base.hash {
		mode := pickMode(base.mode, ours.mode, theirs.mode, true)
		if mode == ours.mode {
			return keepResult(name, ours), nil
		}
		return &mergeResult{name: name, action: mergeWrite, mode: mode, hash: ours.hash}, nil
	}
	return w.planContentMerge(name, base, ours, theirs, label)
}

func (w *Worktree) planAddAdd(name string, ours, theirs sideFile, label string) (*mergeResult, error) {
	stages := stagesFor(sideFile{}, false, ours, true, theirs, true)
	if !sameFileKind(ours.mode, theirs.mode) || ours.mode == filemode.Submodule || theirs.mode == filemode.Submodule {
		return w.planNonTextConflict(name, sideFile{}, false, ours, theirs), nil
	}

	ob, err := w.readBlob(ours.hash)
	if err != nil {
		return nil, err
	}
	tb, err := w.readBlob(theirs.hash)
	if err != nil {
		return nil, err
	}
	if bytes.Equal(ob, tb) || isBinary(ob) || isBinary(tb) {
		return &mergeResult{
			name:         name,
			action:       mergeConflict,
			keepWorktree: true,
			mode:         ours.mode,
			hash:         ours.hash,
			stages:       stages,
		}, nil
	}

	merged, conflicted := mergeText("", string(ob), string(tb), label)
	if !conflicted {
		merged = joinLines(conflictLines(splitLines(string(ob)), splitLines(string(tb)), label))
	}
	mode := ours.mode
	if mode == filemode.Symlink || !sameFileKind(ours.mode, theirs.mode) {
		mode = filemode.Regular
	}
	return &mergeResult{
		name:         name,
		action:       mergeConflict,
		writeContent: true,
		mode:         mode,
		content:      []byte(merged),
		stages:       stages,
	}, nil
}

func (w *Worktree) planContentMerge(name string, base, ours, theirs sideFile, label string) (*mergeResult, error) {
	bb, err := w.readBlob(base.hash)
	if err != nil {
		return nil, err
	}
	ob, err := w.readBlob(ours.hash)
	if err != nil {
		return nil, err
	}
	tb, err := w.readBlob(theirs.hash)
	if err != nil {
		return nil, err
	}
	mode := pickMode(base.mode, ours.mode, theirs.mode, true)
	if isBinary(bb) || isBinary(ob) || isBinary(tb) {
		return w.planNonTextConflict(name, base, true, ours, theirs), nil
	}

	merged, conflict := mergeText(string(bb), string(ob), string(tb), label)
	if conflict {
		if mode == filemode.Symlink {
			mode = filemode.Regular
		}
		return &mergeResult{
			name:         name,
			action:       mergeConflict,
			mode:         mode,
			writeContent: true,
			content:      []byte(merged),
			stages:       stagesFor(base, true, ours, true, theirs, true),
		}, nil
	}

	mergedBytes := []byte(merged)
	switch {
	case bytes.Equal(mergedBytes, ob) && mode == ours.mode:
		return keepResult(name, ours), nil
	case bytes.Equal(mergedBytes, ob):
		return &mergeResult{name: name, action: mergeWrite, mode: mode, hash: ours.hash}, nil
	case bytes.Equal(mergedBytes, tb):
		return &mergeResult{name: name, action: mergeWrite, mode: mode, hash: theirs.hash}, nil
	default:
		return &mergeResult{
			name:         name,
			action:       mergeWrite,
			mode:         mode,
			writeContent: true,
			content:      mergedBytes,
		}, nil
	}
}

func (w *Worktree) planNonTextConflict(name string, base sideFile, hasBase bool, ours, theirs sideFile) *mergeResult {
	return &mergeResult{
		name:         name,
		action:       mergeConflict,
		mode:         ours.mode,
		hash:         ours.hash,
		keepWorktree: true,
		stages:       stagesFor(base, hasBase, ours, true, theirs, true),
	}
}

func keepResult(name string, f sideFile) *mergeResult {
	return &mergeResult{
		name:   name,
		action: mergeKeep,
		mode:   f.mode,
		hash:   f.hash,
	}
}

func stagesFor(base sideFile, hasBase bool, ours sideFile, hasOurs bool, theirs sideFile, hasTheirs bool) []stageBlob {
	stages := make([]stageBlob, 0, 3)
	if hasBase {
		stages = append(stages, stageBlob{stage: index.AncestorMode, mode: base.mode, hash: base.hash})
	}
	if hasOurs {
		stages = append(stages, stageBlob{stage: index.OurMode, mode: ours.mode, hash: ours.hash})
	}
	if hasTheirs {
		stages = append(stages, stageBlob{stage: index.TheirMode, mode: theirs.mode, hash: theirs.hash})
	}
	return stages
}

func sameFileKind(a, b filemode.FileMode) bool {
	return fileKind(a) == fileKind(b)
}

func fileKind(m filemode.FileMode) int {
	switch m {
	case filemode.Symlink:
		return 1
	case filemode.Submodule:
		return 2
	case filemode.Dir:
		return 3
	default:
		return 0
	}
}

func pickMode(base, ours, theirs filemode.FileMode, hasBase bool) filemode.FileMode {
	if ours == theirs || !hasBase {
		return ours
	}
	switch {
	case ours != base && theirs == base:
		return ours
	case theirs != base && ours == base:
		return theirs
	default:
		return ours
	}
}

func isBinary(b []byte) bool {
	return bytes.IndexByte(b, 0) >= 0
}

func (w *Worktree) applyMerge(idx *index.Index, results []*mergeResult) error {
	for _, res := range results {
		switch res.action {
		case mergeDelete:
			if err := w.deleteMergedPath(res.name); err != nil {
				return err
			}
		case mergeConflict:
			if res.rename != "" {
				if err := w.removeFileIfPresent(res.name); err != nil {
					return err
				}
			}
		}
	}

	for _, res := range results {
		if err := w.applyMergeResult(idx, res); err != nil {
			return err
		}
	}

	sort.Slice(idx.Entries, func(i, j int) bool {
		if idx.Entries[i].Name != idx.Entries[j].Name {
			return idx.Entries[i].Name < idx.Entries[j].Name
		}
		return idx.Entries[i].Stage < idx.Entries[j].Stage
	})
	return nil
}

func (w *Worktree) applyMergeResult(idx *index.Index, res *mergeResult) error {
	switch res.action {
	case mergeKeep:
		if e := matchingStage0(idx, res.name); e != nil && e.Hash == res.hash && e.Mode == res.mode {
			return nil
		}
		return w.stageMerged(idx, res.name, res.hash, res.mode)
	case mergeDelete:
		removeIndexEntries(idx, res.name)
		return nil
	case mergeWrite:
		return w.applyMergeWrite(idx, res)
	case mergeConflict:
		return w.applyMergeConflict(idx, res)
	default:
		return fmt.Errorf("unknown merge action for %s", res.name)
	}
}

func (w *Worktree) applyMergeWrite(idx *index.Index, res *mergeResult) error {
	if res.mode == filemode.Submodule && !res.writeContent {
		return w.stageSubmodule(idx, res.name, res.hash)
	}

	hash := res.hash
	var err error
	if res.writeContent {
		hash, err = w.saveBlob(res.content)
		if err != nil {
			return err
		}
		if err = w.writeWorktreeFile(res.name, res.mode, res.content); err != nil {
			return err
		}
	} else if err = w.checkoutBlob(res.name, res.mode, hash); err != nil {
		return err
	}
	return w.stageMerged(idx, res.name, hash, res.mode)
}

func (w *Worktree) applyMergeConflict(idx *index.Index, res *mergeResult) error {
	dest := res.name
	if res.rename != "" {
		dest = res.rename
	}
	removeIndexEntries(idx, res.name)
	if dest != res.name {
		removeIndexEntries(idx, dest)
	}

	switch {
	case res.writeContent:
		if err := w.writeWorktreeFile(dest, res.mode, res.content); err != nil {
			return err
		}
	case !res.keepWorktree:
		if res.mode == filemode.Submodule {
			if err := w.Filesystem.MkdirAll(dest, 0o755); err != nil {
				return err
			}
		} else if err := w.checkoutBlob(dest, res.mode, res.hash); err != nil {
			return err
		}
	}

	for _, st := range res.stages {
		addConflictStage(idx, dest, st)
	}
	return nil
}

func (w *Worktree) stageMerged(idx *index.Index, name string, hash plumbing.Hash, mode filemode.FileMode) error {
	removeIndexEntries(idx, name)
	e := idx.Add(name)
	if err := w.doUpdateFileToIndex(e, name, hash); err != nil {
		return err
	}
	e.Mode = mode
	e.Stage = 0
	return nil
}

func (w *Worktree) stageSubmodule(idx *index.Index, name string, hash plumbing.Hash) error {
	removeIndexEntries(idx, name)
	if err := w.Filesystem.MkdirAll(name, 0o755); err != nil {
		return err
	}
	e := idx.Add(name)
	e.Hash = hash
	e.Mode = filemode.Submodule
	e.Stage = 0
	return nil
}

func (w *Worktree) deleteMergedPath(name string) error {
	fi, err := w.Filesystem.Lstat(name)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if fi.IsDir() {
		return nil
	}
	return rmFileAndDirsIfEmpty(w.Filesystem, name)
}

func (w *Worktree) removeFileIfPresent(name string) error {
	fi, err := w.Filesystem.Lstat(name)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if fi.IsDir() {
		return nil
	}
	return w.Filesystem.Remove(name)
}

func (w *Worktree) checkoutBlob(name string, mode filemode.FileMode, hash plumbing.Hash) error {
	data, err := w.readBlob(hash)
	if err != nil {
		return err
	}
	return w.writeWorktreeFile(name, mode, data)
}

func (w *Worktree) writeWorktreeFile(name string, mode filemode.FileMode, data []byte) (err error) {
	if err = w.prepareWorktreePath(name); err != nil {
		return err
	}

	fi, statErr := w.Filesystem.Lstat(name)
	if statErr == nil && (fi.IsDir() || fi.Mode()&os.ModeSymlink != 0) {
		if fi.IsDir() {
			if err = util.RemoveAll(w.Filesystem, name); err != nil {
				return err
			}
		} else if err = w.Filesystem.Remove(name); err != nil {
			return err
		}
	}

	if mode == filemode.Symlink {
		return w.Filesystem.Symlink(string(data), name)
	}

	perm := os.FileMode(0o644)
	if mode == filemode.Executable {
		perm = 0o755
	}
	f, err := w.Filesystem.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	defer ioutil.CheckClose(f, &err)
	_, err = f.Write(data)
	return err
}

func (w *Worktree) prepareWorktreePath(name string) error {
	dir := path.Dir(name)
	if dir != "." && dir != "" && dir != "/" {
		parts := strings.Split(dir, "/")
		cur := ""
		for _, part := range parts {
			if cur == "" {
				cur = part
			} else {
				cur = cur + "/" + part
			}
			fi, err := w.Filesystem.Lstat(cur)
			if err != nil {
				continue
			}
			if fi.IsDir() {
				continue
			}
			if err := w.Filesystem.Remove(cur); err != nil {
				return err
			}
		}
		if err := w.Filesystem.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}

	fi, err := w.Filesystem.Lstat(name)
	if err != nil {
		return nil
	}
	if fi.IsDir() {
		return util.RemoveAll(w.Filesystem, name)
	}
	return nil
}

func (w *Worktree) readBlob(h plumbing.Hash) (data []byte, err error) {
	blob, err := object.GetBlob(w.r.Storer, h)
	if err != nil {
		return nil, err
	}
	r, err := blob.Reader()
	if err != nil {
		return nil, err
	}
	defer ioutil.CheckClose(r, &err)
	return io.ReadAll(r)
}

func (w *Worktree) saveBlob(data []byte) (plumbing.Hash, error) {
	obj := w.r.Storer.NewEncodedObject()
	obj.SetType(plumbing.BlobObject)
	obj.SetSize(int64(len(data)))

	writer, err := obj.Writer()
	if err != nil {
		return plumbing.ZeroHash, err
	}
	if _, err = writer.Write(data); err != nil {
		_ = writer.Close()
		return plumbing.ZeroHash, err
	}
	if err = writer.Close(); err != nil {
		return plumbing.ZeroHash, err
	}
	return w.r.Storer.SetEncodedObject(obj)
}

func matchingStage0(idx *index.Index, name string) *index.Entry {
	name = indexPath(name)
	var found *index.Entry
	count := 0
	for _, e := range idx.Entries {
		if e.Name != name {
			continue
		}
		count++
		found = e
	}
	if count == 1 && found != nil && found.Stage == 0 {
		return found
	}
	return nil
}

func addConflictStage(idx *index.Index, name string, st stageBlob) {
	e := idx.Add(name)
	e.Hash = st.hash
	e.Mode = st.mode
	e.Stage = st.stage
}

func removeIndexEntries(idx *index.Index, name string) {
	name = indexPath(name)
	n := 0
	for _, e := range idx.Entries {
		if e.Name == name {
			continue
		}
		idx.Entries[n] = e
		n++
	}
	for i := n; i < len(idx.Entries); i++ {
		idx.Entries[i] = nil
	}
	idx.Entries = idx.Entries[:n]
}

func indexHasUnmerged(idx *index.Index) bool {
	for _, e := range idx.Entries {
		if e.Stage != 0 {
			return true
		}
	}
	return false
}

func indexPathHasUnmerged(idx *index.Index, name string) bool {
	name = indexPath(name)
	for _, e := range idx.Entries {
		if e.Name == name && e.Stage != 0 {
			return true
		}
	}
	return false
}

func indexPath(name string) string {
	return strings.ReplaceAll(name, `\`, "/")
}

func (w *Worktree) readMergeHead() (h plumbing.Hash, ok bool, err error) {
	f, err := w.Filesystem.Open(mergeHeadFile)
	if err != nil {
		if os.IsNotExist(err) {
			return plumbing.ZeroHash, false, nil
		}
		return plumbing.ZeroHash, false, err
	}
	defer ioutil.CheckClose(f, &err)

	data, err := io.ReadAll(f)
	if err != nil {
		return plumbing.ZeroHash, false, err
	}
	line := strings.TrimSpace(string(data))
	if line == "" {
		return plumbing.ZeroHash, false, nil
	}
	parsed, valid := plumbing.FromHex(line)
	if !valid {
		return plumbing.ZeroHash, false, fmt.Errorf("invalid %s", mergeHeadFile)
	}
	return parsed, true, nil
}

func (w *Worktree) writeMergeHead(h plumbing.Hash) (err error) {
	if err = w.Filesystem.MkdirAll(".git", 0o755); err != nil {
		return err
	}
	f, err := w.Filesystem.OpenFile(mergeHeadFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer ioutil.CheckClose(f, &err)
	_, err = io.WriteString(f, h.String()+"\n")
	return err
}

func (w *Worktree) removeMergeHead() error {
	err := w.Filesystem.Remove(mergeHeadFile)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// appendMergeHead records .git/MERGE_HEAD as an additional parent when a merge
// is in progress. The file itself is removed only after the commit succeeds.
func (w *Worktree) appendMergeHead(opts *CommitOptions) (bool, error) {
	h, ok, err := w.readMergeHead()
	if err != nil || !ok {
		return false, err
	}
	for _, parent := range opts.Parents {
		if parent.Equal(h) {
			return true, nil
		}
	}
	opts.Parents = append(opts.Parents, h)
	return true, nil
}
