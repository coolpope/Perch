// Package workspace reads and writes the files of an opened folder. Writes
// are atomic and refuse to replace content the caller has not seen, because
// coding agents change the same files while Perch has them open.
package workspace

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"
)

// MaxTextFileBytes is the largest file Perch opens as text.
const MaxTextFileBytes int64 = 20 << 20

const defaultFileMode fs.FileMode = 0o644

var (
	// ErrBinaryFile reports a file that is not UTF-8 text.
	ErrBinaryFile = errors.New("file is not UTF-8 text")
	// ErrFileTooLarge reports a file above MaxTextFileBytes.
	ErrFileTooLarge = errors.New("file is too large to open")
	// ErrChangedOnDisk reports a file that differs from the version the
	// caller loaded.
	ErrChangedOnDisk = errors.New("file changed on disk")
	// ErrOutsideWorkspace reports a path that leaves the opened folder.
	ErrOutsideWorkspace = errors.New("path is outside the workspace")
)

// FileTooLargeError reports the size of a file above the limit. It matches
// ErrFileTooLarge.
type FileTooLargeError struct {
	Path  string
	Size  int64
	Limit int64
}

func (e *FileTooLargeError) Error() string {
	return fmt.Sprintf("read %q (%d bytes, limit %d): %v", e.Path, e.Size, e.Limit, ErrFileTooLarge)
}

// Is reports whether target is ErrFileTooLarge.
func (e *FileTooLargeError) Is(target error) bool {
	return target == ErrFileTooLarge
}

// Digest identifies the exact bytes of a file.
type Digest [sha256.Size]byte

// Entry is a file or directory of the workspace.
type Entry struct {
	// Path is relative to the workspace root and separated by slashes.
	Path  string
	Name  string
	IsDir bool
}

// TextFile is the content of a text file as read from disk.
type TextFile struct {
	Text   string
	Digest Digest
}

// DigestOf returns the digest of data.
func DigestOf(data []byte) Digest {
	return sha256.Sum256(data)
}

// IsIgnored reports whether a slash-separated workspace path is hidden from
// the file tree and the file watcher.
func IsIgnored(relPath string) bool {
	return slices.ContainsFunc(strings.Split(relPath, "/"), isIgnoredName)
}

func isIgnoredName(name string) bool {
	return name == ".git" || name == "node_modules" || name == ".DS_Store"
}

// ListDir returns the visible entries of a directory, directories first and
// each group sorted by name without regard to case.
func ListDir(root string, relDir string) ([]Entry, error) {
	dir, err := resolve(root, relDir)
	if err != nil {
		return nil, err
	}
	items, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("list directory %q: %w", relDir, err)
	}
	entries := make([]Entry, 0, len(items))
	for _, item := range items {
		if isIgnoredName(item.Name()) {
			continue
		}
		entries = append(entries, Entry{
			Path:  path.Join(relDir, item.Name()),
			Name:  item.Name(),
			IsDir: isDirectory(dir, item),
		})
	}
	slices.SortFunc(entries, compareEntries)
	return entries, nil
}

// isDirectory follows symlinks, so a link to a directory opens as one. A
// broken link is listed as a file, and opening it reports the error.
func isDirectory(dir string, item fs.DirEntry) bool {
	if item.Type()&fs.ModeSymlink == 0 {
		return item.IsDir()
	}
	info, err := os.Stat(filepath.Join(dir, item.Name()))
	return err == nil && info.IsDir()
}

func compareEntries(a Entry, b Entry) int {
	if a.IsDir != b.IsDir {
		if a.IsDir {
			return -1
		}
		return 1
	}
	return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
}

// ReadText reads a UTF-8 text file of the workspace.
func ReadText(root string, relPath string) (TextFile, error) {
	file, err := resolve(root, relPath)
	if err != nil {
		return TextFile{}, err
	}
	info, err := os.Stat(file)
	if err != nil {
		return TextFile{}, fmt.Errorf("read %q: %w", relPath, err)
	}
	if info.Size() > MaxTextFileBytes {
		return TextFile{}, &FileTooLargeError{Path: relPath, Size: info.Size(), Limit: MaxTextFileBytes}
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return TextFile{}, fmt.Errorf("read %q: %w", relPath, err)
	}
	if bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
		return TextFile{}, fmt.Errorf("read %q: %w", relPath, ErrBinaryFile)
	}
	return TextFile{Text: string(data), Digest: DigestOf(data)}, nil
}

// WriteText replaces a file whose content on disk still has the digest
// expected, and returns the digest of the new content. It returns
// ErrChangedOnDisk when the file differs or no longer exists.
func WriteText(root string, relPath string, data []byte, expected Digest) (Digest, error) {
	file, err := resolve(root, relPath)
	if err != nil {
		return Digest{}, err
	}
	current, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		return Digest{}, fmt.Errorf("write %q: %w: %w", relPath, ErrChangedOnDisk, err)
	}
	if err != nil {
		return Digest{}, fmt.Errorf("write %q: %w", relPath, err)
	}
	if DigestOf(current) != expected {
		return Digest{}, fmt.Errorf("write %q: %w", relPath, ErrChangedOnDisk)
	}
	return replaceFile(file, relPath, data)
}

// OverwriteText writes a file whatever its content on disk, creating it and
// its directory when they no longer exist.
func OverwriteText(root string, relPath string, data []byte) (Digest, error) {
	file, err := resolve(root, relPath)
	if err != nil {
		return Digest{}, err
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return Digest{}, fmt.Errorf("write %q: %w", relPath, err)
	}
	return replaceFile(file, relPath, data)
}

// replaceFile writes through a temporary file and a rename, so that a failed
// write leaves the old content and readers never see half a file.
func replaceFile(file string, relPath string, data []byte) (Digest, error) {
	target, mode, err := writeTarget(file)
	if err != nil {
		return Digest{}, fmt.Errorf("write %q: %w", relPath, err)
	}
	temp, err := os.CreateTemp(filepath.Dir(target), ".perch-*.tmp")
	if err != nil {
		return Digest{}, fmt.Errorf("write %q: %w", relPath, err)
	}
	if err := fillTemp(temp, data, mode); err != nil {
		return Digest{}, errors.Join(fmt.Errorf("write %q: %w", relPath, err), removeTemp(temp.Name()))
	}
	if err := os.Rename(temp.Name(), target); err != nil {
		return Digest{}, errors.Join(fmt.Errorf("write %q: %w", relPath, err), removeTemp(temp.Name()))
	}
	return DigestOf(data), nil
}

// writeTarget follows a symlink to the file it names, so that saving keeps
// the link, and returns the permissions the new content takes.
func writeTarget(file string) (string, fs.FileMode, error) {
	target, err := filepath.EvalSymlinks(file)
	if errors.Is(err, fs.ErrNotExist) {
		return file, defaultFileMode, nil
	}
	if err != nil {
		return "", 0, err
	}
	info, err := os.Stat(target)
	if err != nil {
		return "", 0, err
	}
	return target, info.Mode().Perm(), nil
}

func fillTemp(temp *os.File, data []byte, mode fs.FileMode) error {
	_, writeErr := temp.Write(data)
	syncErr := temp.Sync()
	closeErr := temp.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return err
	}
	return os.Chmod(temp.Name(), mode)
}

func removeTemp(name string) error {
	if err := os.Remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove temporary file %q: %w", name, err)
	}
	return nil
}

func resolve(root string, relPath string) (string, error) {
	local := filepath.FromSlash(relPath)
	if !filepath.IsLocal(local) {
		return "", fmt.Errorf("resolve %q under %q: %w", relPath, root, ErrOutsideWorkspace)
	}
	return filepath.Join(root, local), nil
}
