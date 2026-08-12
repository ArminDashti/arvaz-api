package fsx

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Entry struct {
	Name       string    `json:"name"`
	Path       string    `json:"path"`
	IsDir      bool      `json:"isDir"`
	SizeBytes  int64     `json:"sizeBytes"`
	CreatedAt  time.Time `json:"createdAt"`
	ModifiedAt time.Time `json:"modifiedAt"`
}

type Client struct {
	Root          string
	MaxUploadBytes int64
}

func New(root string, maxUploadMB int) *Client {
	return &Client{
		Root:           filepath.Clean(root),
		MaxUploadBytes: int64(maxUploadMB) * 1024 * 1024,
	}
}

func (c *Client) Resolve(rel string) (string, error) {
	rel = strings.TrimSpace(rel)
	if rel == "" || rel == "." {
		return c.Root, nil
	}
	rel = filepath.Clean(strings.TrimPrefix(rel, "/"))
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("path escapes root")
	}
	abs := filepath.Join(c.Root, rel)
	abs = filepath.Clean(abs)
	if abs != c.Root && !strings.HasPrefix(abs, c.Root+string(os.PathSeparator)) {
		return "", fmt.Errorf("path escapes root")
	}
	return abs, nil
}

func (c *Client) List(_ context.Context, rel string) ([]Entry, string, error) {
	abs, err := c.Resolve(rel)
	if err != nil {
		return nil, "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, "", err
	}
	if !info.IsDir() {
		return nil, "", fmt.Errorf("not a directory")
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return nil, "", err
	}
	out := make([]Entry, 0, len(entries))
	for _, e := range entries {
		full := filepath.Join(abs, e.Name())
		fi, err := os.Stat(full)
		if err != nil {
			continue
		}
		size := fi.Size()
		if fi.IsDir() {
			size = dirSize(full)
		}
		out = append(out, Entry{
			Name:       e.Name(),
			Path:       relPath(c.Root, full),
			IsDir:      fi.IsDir(),
			SizeBytes:  size,
			CreatedAt:  birthTime(fi),
			ModifiedAt: fi.ModTime().UTC(),
		})
	}
	return out, relPath(c.Root, abs), nil
}

func (c *Client) Mkdir(_ context.Context, rel, name string) error {
	name = strings.TrimSpace(name)
	if name == "" || strings.Contains(name, "/") || strings.Contains(name, `\`) {
		return fmt.Errorf("invalid folder name")
	}
	parent, err := c.Resolve(rel)
	if err != nil {
		return err
	}
	return os.Mkdir(filepath.Join(parent, name), 0o755)
}

func (c *Client) Upload(_ context.Context, rel, filename string, r io.Reader) error {
	filename = strings.TrimSpace(filename)
	if filename == "" || strings.Contains(filename, "/") || strings.Contains(filename, `\`) {
		return fmt.Errorf("invalid filename")
	}
	parent, err := c.Resolve(rel)
	if err != nil {
		return err
	}
	dst := filepath.Join(parent, filename)
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()
	limited := io.LimitReader(r, c.MaxUploadBytes+1)
	n, err := io.Copy(f, limited)
	if err != nil {
		_ = os.Remove(dst)
		return err
	}
	if n > c.MaxUploadBytes {
		_ = os.Remove(dst)
		return fmt.Errorf("file exceeds max upload size")
	}
	return nil
}

func (c *Client) Delete(_ context.Context, rel string) error {
	abs, err := c.Resolve(rel)
	if err != nil {
		return err
	}
	if abs == c.Root {
		return fmt.Errorf("cannot delete root")
	}
	info, err := os.Stat(abs)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return os.RemoveAll(abs)
	}
	return os.Remove(abs)
}

func relPath(root, abs string) string {
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return ""
	}
	return filepath.ToSlash(rel)
}

func dirSize(path string) int64 {
	var total int64
	_ = filepath.WalkDir(path, func(_ string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if fi, err := d.Info(); err == nil {
			total += fi.Size()
		}
		return nil
	})
	return total
}

func birthTime(fi os.FileInfo) time.Time {
	type stater interface {
		Stat() (interface{}, error)
	}
	// ModTime fallback on Linux
	return fi.ModTime().UTC()
}
