package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// The app was called Btelo Video Cut, and kept its settings, unfinished edits and downloads in folders called
// btelo-video-cut. They move to the new name's folders the first time it runs; the unfinished edits name pasted
// pictures and downloaded music by their full paths, so those paths are rewritten to match.

// dataName is the folder name the app keeps its data under, and oldDataName the one it had before.
const (
	dataName    = "btelo-forge"
	oldDataName = "btelo-video-cut"
)

// dataDir is the folder under parent for the app's data, with what was moved into it from the old one: the old
// folder renamed when there is no new one, or, when both are there, everything of the old one the new one lacks or
// has an older copy of.
// moves maps each old path that moved to its new path.
//
// Whatever cannot be moved (another copy of the old app has it open) stays where it was, still found there by the
// paths that name it, and is moved on a later run. When the whole old folder cannot be renamed, it goes on being
// used, so nothing is lost.
func dataDir(parent string) (dir string, moves map[string]string) {
	dir, old := filepath.Join(parent, dataName), filepath.Join(parent, oldDataName)
	moves = map[string]string{}
	if _, err := os.Stat(old); err != nil {
		return dir, moves
	}
	if _, err := os.Stat(dir); err != nil {
		if err := os.Rename(old, dir); err != nil {
			return old, moves
		}
		moves[old] = dir
		return dir, moves
	}
	merge(old, dir, moves)
	os.Remove(old) // only when everything left it
	return dir, moves
}

// merge moves into dir what of old it lacks, folder by folder. Of a file in both, the newer copy is kept in dir; an
// older one from the old folder stays where it was.
func merge(old, dir string, moves map[string]string) {
	entries, err := os.ReadDir(old)
	if err != nil {
		return
	}
	for _, e := range entries {
		from, to := filepath.Join(old, e.Name()), filepath.Join(dir, e.Name())
		info, err := os.Lstat(to)
		switch {
		case errors.Is(err, os.ErrNotExist):
			if os.Rename(from, to) == nil {
				moves[from] = to
			}
		case err == nil && info.IsDir() && e.IsDir():
			merge(from, to, moves)
			os.Remove(from)
		case err == nil && !info.IsDir() && e.Type().IsRegular():
			if mine, err := e.Info(); err == nil && mine.ModTime().After(info.ModTime()) && os.Rename(from, to) == nil {
				moves[from] = to
			}
		}
	}
}

// rewritePaths changes, in every edit saved in dir, the paths of what moved: a moved folder's path where a path
// begins with it, a moved file's where it is the whole path. Each file is replaced whole, so a failure leaves it as
// it was.
func rewritePaths(dir string, moves map[string]string) error {
	if len(moves) == 0 {
		return nil
	}
	var pairs [][2][]byte
	for old, now := range moves {
		p, err := pathForms(old, now)
		if err != nil {
			return err
		}
		pairs = append(pairs, p...)
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return err
	}
	var errs []error
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		out := data
		for _, p := range pairs {
			out = bytes.ReplaceAll(out, p[0], p[1])
		}
		if bytes.Equal(out, data) {
			continue
		}
		tmp := f + ".moving"
		if err := os.WriteFile(tmp, out, 0o644); err != nil {
			errs = append(errs, err)
			continue
		}
		if err := os.Rename(tmp, f); err != nil {
			os.Remove(tmp)
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// pathForms are the ways a moved path appears in a saved edit, as JSON writes it, each with what replaces it: the
// path followed by a separator, as paths within a folder begin, and the whole path in its quotes. Neither matches a
// path that merely begins with the same letters.
func pathForms(old, now string) ([][2][]byte, error) {
	quoted := func(s string) ([]byte, error) { return json.Marshal(s) }
	sep := string(filepath.Separator)
	o, err := quoted(old + sep)
	if err != nil {
		return nil, err
	}
	n, err := quoted(now + sep)
	if err != nil {
		return nil, err
	}
	whole, err := quoted(old)
	if err != nil {
		return nil, err
	}
	wholeNow, err := quoted(now)
	if err != nil {
		return nil, err
	}
	return [][2][]byte{
		{bytes.TrimSuffix(o, []byte(`"`)), bytes.TrimSuffix(n, []byte(`"`))}, // `"…\old\` starts a longer path
		{whole, wholeNow},
	}, nil
}
