package main

import (
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
)

// media_delivery=local keeps every downloaded media file on disk and records
// its path in message_history.media_link, for single-host consumers (such as a
// read-only archive server) that must not depend on WhatsApp's CDN keeping
// the file. The path is never put in webhook or event payloads.

var mediaDir = flag.String("mediadir", "", "Directory for media_delivery=local (defaults to <datadir>/media)")

func localMediaRoot() string {
	if *mediaDir != "" {
		return *mediaDir
	}
	base := *dataDir
	if base == "" {
		if ex, err := os.Executable(); err == nil {
			base = filepath.Dir(ex)
		}
	}
	return filepath.Join(base, "media")
}

// saveLocalMedia copies media into root as name (already messageID+ext) and
// returns the final path. The write goes through a temp file and rename, so a
// reader never sees a partial file.
func saveLocalMedia(root string, media *mediaFile, name string) (string, error) {
	name = filepath.Base(name)
	if name == "" || name == "." || name == ".." || name == string(filepath.Separator) {
		return "", errors.New("invalid local media name")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	src, err := media.Open()
	if err != nil {
		return "", err
	}
	defer src.Close()
	tmp, err := os.CreateTemp(root, ".partial-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	if _, err = io.Copy(tmp, src); err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return "", err
	}
	dst := filepath.Join(root, name)
	if err = os.Rename(tmp.Name(), dst); err != nil {
		return "", err
	}
	return dst, nil
}
