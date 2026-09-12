// Package lab contains the opt-in integration laboratory, not product behavior.
package lab

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"
)

const sourceRoot = "/tmp/source"
const sourceReady = "/tmp/source.ready"

// WriteSourceArchive reads only the screened private snapshot, never the live
// checkout. Modes and bytes are preserved; ownership is supplied by the receiver.
func WriteSourceArchive(root string, s Source, out io.Writer) error {
	if err := VerifySource(root, s); err != nil {
		return err
	}
	w := tar.NewWriter(out)
	for _, f := range s.Files {
		b, err := os.ReadFile(filepath.Join(root, f.Path))
		if err != nil {
			return errors.New("source_archive_read")
		}
		if err := w.WriteHeader(&tar.Header{Name: f.Path, Mode: int64(f.Mode), Size: int64(len(b)), Typeflag: tar.TypeReg}); err != nil {
			return errors.New("source_archive_write")
		}
		if _, err := w.Write(b); err != nil {
			return errors.New("source_archive_write")
		}
	}
	b, _ := json.Marshal(s)
	if err := w.WriteHeader(&tar.Header{Name: ".lab-source.json", Mode: 0644, Size: int64(len(b)), Typeflag: tar.TypeReg}); err != nil {
		return errors.New("source_archive_write")
	}
	if _, err := w.Write(b); err != nil {
		return errors.New("source_archive_write")
	}
	return w.Close()
}

// receiveSource runs as the runtime UID. No archive owner, symlink, special file
// or absolute path is accepted, and no host path is mounted or chmodded.
func receiveSource(root string, in io.Reader) error {
	if err := os.Mkdir(root, 0700); err != nil {
		return errors.New("source_receive_directory")
	}
	r := tar.NewReader(io.LimitReader(in, 140<<20))
	total := int64(0)
	count := 0
	for {
		h, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return errors.New("source_archive_invalid")
		}
		count++
		total += h.Size
		if count > 10000 || h.Size < 0 || h.Size > 16<<20 || total > 128<<20 || h.Typeflag != tar.TypeReg || h.Mode < 0 || h.Mode&^0777 != 0 || (!safeSourcePath(h.Name) && h.Name != ".lab-source.json") {
			return errors.New("source_archive_unsafe")
		}
		path := filepath.Join(root, h.Name)
		if os.MkdirAll(filepath.Dir(path), 0700) != nil {
			return errors.New("source_receive_directory")
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return errors.New("source_archive_duplicate")
		}
		_, copyErr := io.CopyN(f, r, h.Size)
		modeErr := f.Chmod(os.FileMode(h.Mode))
		closeErr := f.Close()
		if copyErr != nil || modeErr != nil || closeErr != nil {
			return errors.New("source_receive_write")
		}
	}
	b, err := os.ReadFile(filepath.Join(root, ".lab-source.json"))
	var s Source
	if err != nil || json.Unmarshal(b, &s) != nil {
		return errors.New("source_manifest")
	}
	return VerifySource(root, s)
}

func ReceiveSource(in io.Reader) error {
	if os.Getuid() == 0 {
		return errors.New("nonroot_required")
	}
	if err := receiveSource(sourceRoot, in); err != nil {
		return err
	}
	f, err := os.OpenFile(sourceReady, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return errors.New("source_ready_create")
	}
	_, writeErr := f.WriteString("ready\n")
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil {
		return errors.New("source_ready_write")
	}
	return nil
}

type SourceFile struct {
	Path string `json:"path"`
	Mode uint32 `json:"mode"`
	Hash string `json:"hash"`
}

type Source struct {
	Commit string       `json:"commit"`
	Hash   string       `json:"hash"`
	Dirty  bool         `json:"dirty"`
	Files  []SourceFile `json:"files"`
}

func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func git(ctx context.Context, dir string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, "git", append([]string{"-c", "core.fsmonitor=false", "-C", dir}, args...)...)
	// No global git configuration, credentials, hooks, or environment redirection.
	c.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=/nonexistent", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_OPTIONAL_LOCKS=0"}
	b := &boundedBuffer{limit: 2 << 20}
	c.Stdout = b
	err := c.Run()
	if err != nil {
		return nil, errors.New("source_git")
	}
	return b.data, nil
}

func safeSourcePath(p string) bool {
	if p == "" || p == ".lab-source.json" || !filepath.IsLocal(p) || filepath.ToSlash(filepath.Clean(p)) != p || strings.ContainsAny(p, "\x00\n\r\\:") {
		return false
	}
	for _, part := range strings.Split(p, "/") {
		lower := strings.ToLower(part)
		if lower == ".git" || lower == "node_modules" || lower == ".codegraph" || lower == "credentials" || lower == "auth.json" || lower == ".env" || strings.HasPrefix(lower, ".env.") || strings.HasSuffix(lower, ".pem") || strings.HasSuffix(lower, ".key") || strings.HasSuffix(lower, ".db") || strings.HasSuffix(lower, ".sqlite") {
			return false
		}
	}
	return true
}

func sourcePaths(ctx context.Context, root string) ([]string, error) {
	b, err := git(ctx, root, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	unique := map[string]bool{}
	for _, p := range strings.Split(string(b), "\x00") {
		if p == "" {
			continue
		}
		if !safeSourcePath(p) {
			return nil, errors.New("source_unsafe_path")
		}
		unique[p] = true
	}
	paths := make([]string, 0, len(unique))
	for p := range unique {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths, nil
}

// scanSource refuses all symlinks, including symlinked ancestors. Deleted tracked
// files remain deleted; nonignored untracked files participate in the identity.
func scanSource(root string, paths []string, destination string) ([]SourceFile, error) {
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, errors.New("source_open")
	}
	defer r.Close()
	files := []SourceFile{}
	var total int64
	for _, p := range paths {
		parts := strings.Split(p, "/")
		missing := false
		for i := range parts {
			st, err := r.Lstat(strings.Join(parts[:i+1], "/"))
			if errors.Is(err, fs.ErrNotExist) {
				missing = true
				break
			}
			if err != nil || st.Mode()&os.ModeSymlink != 0 {
				return nil, errors.New("source_symlink_or_io")
			}
		}
		if missing {
			continue
		}
		st, err := r.Stat(p)
		if err != nil || !st.Mode().IsRegular() || st.Size() > 16<<20 {
			return nil, errors.New("source_file_bound")
		}
		total += st.Size()
		if total > 128<<20 {
			return nil, errors.New("source_total_bound")
		}
		b, err := r.ReadFile(p)
		if err != nil {
			return nil, errors.New("source_read")
		}
		f := SourceFile{p, uint32(st.Mode().Perm()), digest(b)}
		files = append(files, f)
		if destination != "" {
			path := filepath.Join(destination, p)
			if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				return nil, errors.New("source_copy")
			}
			if err := os.WriteFile(path, b, st.Mode().Perm()); err != nil {
				return nil, errors.New("source_copy")
			}
			if err := os.Chmod(path, st.Mode().Perm()); err != nil {
				return nil, errors.New("source_copy_mode")
			}
		}
	}
	return files, nil
}

func Snapshot(ctx context.Context, root, destination string) (Source, error) {
	var s Source
	commit, err := git(ctx, root, "rev-parse", "HEAD")
	if err != nil {
		return s, err
	}
	status, err := git(ctx, root, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return s, err
	}
	paths, err := sourcePaths(ctx, root)
	if err != nil {
		return s, err
	}
	files, err := scanSource(root, paths, destination)
	if err != nil {
		return s, err
	}
	againPaths, err := sourcePaths(ctx, root)
	if err != nil {
		return s, err
	}
	again, err := scanSource(root, againPaths, "")
	if err != nil {
		return s, err
	}
	endCommit, err := git(ctx, root, "rev-parse", "HEAD")
	if err != nil {
		return s, err
	}
	endStatus, err := git(ctx, root, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return s, err
	}
	if !reflect.DeepEqual(files, again) || string(commit) != string(endCommit) || string(status) != string(endStatus) {
		return s, errors.New("source_changed_during_capture")
	}
	b, _ := json.Marshal(files)
	s = Source{strings.TrimSpace(string(commit)), digest(b), len(status) > 0, files}
	if destination != "" {
		b, _ = json.Marshal(s)
		err = os.WriteFile(filepath.Join(destination, ".lab-source.json"), b, 0644)
	}
	return s, err
}

func VerifySource(root string, s Source) error {
	paths := make([]string, len(s.Files))
	for i, f := range s.Files {
		if !safeSourcePath(f.Path) {
			return errors.New("source_manifest_path")
		}
		paths[i] = f.Path
	}
	allowed := map[string]bool{".lab-source.json": true}
	for _, p := range paths {
		allowed[p] = true
	}
	if err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return errors.New("source_walk")
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil || !allowed[filepath.ToSlash(rel)] || d.Type()&os.ModeSymlink != 0 {
			return errors.New("source_extra_file")
		}
		return nil
	}); err != nil {
		return err
	}
	files, err := scanSource(root, paths, "")
	if err != nil {
		return err
	}
	b, _ := json.Marshal(files)
	if digest(b) != s.Hash || !reflect.DeepEqual(files, s.Files) {
		return errors.New("source_identity_mismatch")
	}
	return nil
}
