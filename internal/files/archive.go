package files

import (
	"archive/tar"
	"archive/zip"
	"compress/bzip2"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/btelolabs/btelo-forge/internal/proc"
)

// archiveKinds are the archives Extract reads, by how their names end; the first ones are read here, the others by
// the system's tar, which on Windows 10 and macOS is libarchive and reads 7-Zip and RAR too.
var archiveKinds = []string{".tar.gz", ".tgz", ".tar.bz2", ".tbz2", ".tar", ".zip", ".gz", ".7z", ".rar", ".tar.xz", ".txz", ".xz", ".tar.zst", ".zst", ".bz2"}

// archiveExt is how an archive's name ends, or "" when it is not an archive Extract reads.
func archiveExt(name string) string {
	lower := strings.ToLower(name)
	for _, ext := range archiveKinds {
		if strings.HasSuffix(lower, ext) && len(lower) > len(ext) {
			return ext
		}
	}
	return ""
}

// IsArchive reports whether Extract reads a file of that name.
func IsArchive(name string) bool { return archiveExt(name) != "" }

// ErrUnsafeArchive is an archive with an entry that would be written outside the folder it is extracted to.
var ErrUnsafeArchive = errors.New("the archive has an entry that points outside it")

// Compress packs srcs into a new zip file at dst, the folders with everything in them; when ctx ends, the half
// written file is deleted.
func Compress(ctx context.Context, srcs []string, dst string, p *Progress) (err error) {
	p.Counting.Store(true)
	p.Total.Store(count(ctx, srcs, false))
	p.Counting.Store(false)
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			os.Remove(dst)
		}
	}()
	zw := zip.NewWriter(out)
	for _, src := range srcs {
		base := filepath.Dir(src)
		err = filepath.WalkDir(src, func(path string, d fs.DirEntry, werr error) error {
			if werr != nil {
				return werr
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if SamePath(path, dst) {
				return nil // the archive being written, when it is made inside a folder being packed
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			if isLink(info.Mode()) {
				return nil // a link is not followed, and zip has no portable way to keep it
			}
			rel, _ := filepath.Rel(base, path)
			h, err := zip.FileInfoHeader(info)
			if err != nil {
				return err
			}
			h.Name = filepath.ToSlash(rel)
			if d.IsDir() {
				h.Name += "/"
				_, err = zw.CreateHeader(h)
				return err
			}
			h.Method = zip.Deflate
			w, err := zw.CreateHeader(h)
			if err != nil {
				return err
			}
			p.at(path)
			in, err := os.Open(path)
			if err != nil {
				return err
			}
			defer in.Close()
			_, err = io.Copy(w, &progressReader{ctx: ctx, r: in, p: p})
			return err
		})
		if err != nil {
			zw.Close()
			out.Close()
			return err
		}
	}
	if err = zw.Close(); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// progressReader counts what is read through it, and stops when ctx ends.
type progressReader struct {
	ctx context.Context
	r   io.Reader
	p   *Progress
}

func (pr *progressReader) Read(b []byte) (int, error) {
	if err := pr.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := pr.r.Read(b)
	pr.p.Done.Add(int64(n))
	return n, err
}

// Extract unpacks an archive beside it, as a smart extraction does: an archive holding one folder or file gives just
// that, any other gives a folder named after the archive. Nothing is overwritten: a name already taken gets a free
// one. It returns what it made; when ctx ends or the archive is damaged, nothing is left.
func Extract(ctx context.Context, archive string, p *Progress) (string, error) {
	ext := archiveExt(archive)
	if ext == "" {
		return "", fmt.Errorf("%s is not an archive", filepath.Base(archive))
	}
	dir := filepath.Dir(archive)
	stem := filepath.Base(archive)
	stem = stem[:len(stem)-len(ext)]
	tmp, err := os.MkdirTemp(dir, "."+stem+".extracting-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp) // empty once its contents have been moved out
	info, err := os.Stat(archive)
	if err != nil {
		return "", err
	}
	p.Total.Store(info.Size())
	p.at(archive)
	switch ext {
	case ".zip":
		err = extractZip(ctx, archive, tmp, p)
	case ".tar", ".tar.gz", ".tgz", ".tar.bz2", ".tbz2":
		err = extractTar(ctx, archive, ext, tmp, p)
	case ".gz", ".bz2":
		err = extractSingle(ctx, archive, ext, filepath.Join(tmp, stem), p)
	default:
		err = extractWithTar(ctx, archive, tmp)
	}
	if err != nil {
		return "", err
	}
	des, err := os.ReadDir(tmp)
	if err != nil {
		return "", err
	}
	from, name := tmp, stem
	if len(des) == 1 {
		from, name = filepath.Join(tmp, des[0].Name()), des[0].Name()
	}
	to := filepath.Join(dir, UniqueName(dir, name))
	if from == tmp {
		// The temporary folder itself becomes the result, so the deferred removal finds nothing.
		if err := os.Rename(tmp, to); err != nil {
			return "", err
		}
		return to, nil
	}
	return to, os.Rename(from, to)
}

// inside is where an entry named in an archive goes under root, refusing a name that leads out of it.
func inside(root, name string) (string, error) {
	name = strings.ReplaceAll(name, `\`, "/")
	if filepath.IsAbs(name) || strings.HasPrefix(name, "/") || filepath.VolumeName(name) != "" {
		return "", ErrUnsafeArchive
	}
	path := filepath.Join(root, filepath.FromSlash(name))
	if !Within(path, root) {
		return "", ErrUnsafeArchive
	}
	return path, nil
}

func extractZip(ctx context.Context, archive, root string, p *Progress) error {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer zr.Close()
	var total int64
	for _, f := range zr.File {
		total += int64(f.UncompressedSize64)
	}
	p.Total.Store(total)
	for _, f := range zr.File {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := f.Name
		if f.NonUTF8 || !utf8.ValidString(name) {
			name = decodeLegacyName(name) // written by an older Windows in its own code page
		}
		path, err := inside(root, name)
		if err != nil {
			return err
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(path, 0o755); err != nil {
				return err
			}
			continue
		}
		if f.Mode()&fs.ModeSymlink != 0 {
			continue // a link in an archive could point anywhere: it is not made
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		err = writeFile(ctx, path, rc, f.Modified, p)
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func extractTar(ctx context.Context, archive, ext, root string, p *Progress) error {
	in, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer in.Close()
	var r io.Reader = &progressReader{ctx: ctx, r: in, p: p}
	switch ext {
	case ".tar.gz", ".tgz":
		gz, err := gzip.NewReader(r)
		if err != nil {
			return err
		}
		defer gz.Close()
		r = gz
	case ".tar.bz2", ".tbz2":
		r = bzip2.NewReader(r)
	}
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		path, err := inside(root, h.Name)
		if err != nil {
			return err
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(path, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := writeFile(ctx, path, tr, h.ModTime, &Progress{}); err != nil {
				return err
			}
		}
	}
}

func extractSingle(ctx context.Context, archive, ext, to string, p *Progress) error {
	in, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer in.Close()
	var r io.Reader = &progressReader{ctx: ctx, r: in, p: p}
	mod := time.Time{}
	if ext == ".gz" {
		gz, err := gzip.NewReader(r)
		if err != nil {
			return err
		}
		defer gz.Close()
		r, mod = gz, gz.ModTime
	} else {
		r = bzip2.NewReader(r)
	}
	return writeFile(ctx, to, r, mod, &Progress{})
}

// writeFile writes what r holds to a new file at path, making its folder.
func writeFile(ctx context.Context, path string, r io.Reader, mod time.Time, p *Progress) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, &progressReader{ctx: ctx, r: r, p: p}); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if !mod.IsZero() {
		os.Chtimes(path, mod, mod)
	}
	return nil
}

// extractWithTar has the system's tar unpack what is not read here, 7-Zip and RAR among them.
func extractWithTar(ctx context.Context, archive, root string) error {
	cmd := exec.CommandContext(ctx, systemTar(), "-xf", archive, "-C", root)
	proc.Hide(cmd)
	if out, err := cmd.CombinedOutput(); err != nil {
		if msg := strings.TrimSpace(string(out)); msg != "" {
			return fmt.Errorf("%s", msg)
		}
		return fmt.Errorf("%s cannot be read here: %w", filepath.Base(archive), err)
	}
	// tar checks names itself; this makes sure nothing it wrote is a link leading out.
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err == nil && d.Type()&fs.ModeSymlink != 0 {
			os.Remove(path)
		}
		return nil
	})
}

// Create makes a new empty file, or a folder when the name has no extension, as the file manager's n does; it
// refuses a name already taken.
func Create(dir, name string) (string, error) {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return "", fmt.Errorf("%q is not a file name", name)
	}
	path := filepath.Join(dir, name)
	if filepath.Ext(strings.TrimPrefix(name, ".")) == "" {
		return path, os.Mkdir(path, 0o755)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", err
	}
	return path, f.Close()
}
