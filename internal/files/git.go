package files

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/btelolabs/btelo-forge/internal/proc"
)

// GitStatus is the state of the git repository a folder is in.
type GitStatus struct {
	Root          string
	Branch        string // the branch checked out, or the commit when none is
	Detached      bool
	Ahead, Behind int // commits not pushed and not pulled, against the branch it tracks
	Staged        int // files with changes added for the next commit
	Unstaged      int // tracked files changed and not added
	Untracked     int
	Conflicts     int
	Counted       bool // git was there to count the changes; without it only the branch is known
	changed       int
}

// Changed is how many files have changes not committed, each counted once though it may be both staged and changed
// again since.
func (s GitStatus) Changed() int { return s.changed }

// FindRepo is the root of the git repository dir is in: the nearest folder above it, or itself, with a .git.
func FindRepo(dir string) (string, bool) {
	dir = filepath.Clean(dir)
	for {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return dir, true
		}
		up := filepath.Dir(dir)
		if up == dir {
			return "", false
		}
		dir = up
	}
}

// Git reads the state of the repository at root, asking git when it is installed and reading the branch from the
// repository itself when it is not.
func Git(ctx context.Context, root string) GitStatus {
	s := GitStatus{Root: root}
	cmd := exec.CommandContext(ctx, "git", "-C", root, "--no-optional-locks", "status", "--porcelain=v2", "--branch", "--untracked-files=normal")
	proc.Hide(cmd)
	out, err := cmd.Output()
	if err != nil {
		s.Branch, s.Detached = readHead(root)
		return s
	}
	parseStatus(&s, out)
	return s
}

// parseStatus reads git's porcelain v2 output.
func parseStatus(s *GitStatus, out []byte) {
	s.Counted = true
	oid := ""
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "# branch.head "):
			s.Branch = strings.TrimPrefix(line, "# branch.head ")
			if s.Branch == "(detached)" {
				s.Detached = true
			}
		case strings.HasPrefix(line, "# branch.oid "):
			oid = strings.TrimPrefix(line, "# branch.oid ")
		case strings.HasPrefix(line, "# branch.ab "):
			for _, f := range strings.Fields(strings.TrimPrefix(line, "# branch.ab ")) {
				n, _ := strconv.Atoi(f[1:])
				if f[0] == '+' {
					s.Ahead = n
				} else {
					s.Behind = n
				}
			}
		case strings.HasPrefix(line, "1 "), strings.HasPrefix(line, "2 "):
			if len(line) >= 4 {
				if line[2] != '.' {
					s.Staged++
				}
				if line[3] != '.' {
					s.Unstaged++
				}
			}
			s.changed++
		case strings.HasPrefix(line, "u "):
			s.Conflicts++
			s.changed++
		case strings.HasPrefix(line, "? "):
			s.Untracked++
			s.changed++
		}
	}
	if s.Detached {
		s.Branch = oid[:min(7, len(oid))]
	}
}

// readHead reads the branch from the repository's HEAD, following a .git file to the folder it names, as a
// worktree's does.
func readHead(root string) (string, bool) {
	gitDir := filepath.Join(root, ".git")
	if b, err := os.ReadFile(gitDir); err == nil {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir: "); ok {
			if !filepath.IsAbs(rest) {
				rest = filepath.Join(root, rest)
			}
			gitDir = rest
		}
	}
	b, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return "", false
	}
	head := strings.TrimSpace(string(b))
	if ref, ok := strings.CutPrefix(head, "ref: refs/heads/"); ok {
		return ref, false
	}
	return head[:min(7, len(head))], true
}
