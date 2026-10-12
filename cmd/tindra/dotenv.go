package main

import (
	"errors"
	"io/fs"
	"os"
	"strings"

	"github.com/joho/godotenv"
)

// dotenv applies a .env file beneath the real environment: variables set by
// the process environment (Docker, systemd, the shell) always win. Reloading on
// SIGHUP keeps that order, picks up edited values, and drops removed ones.
type dotenv struct {
	path   string
	real   map[string]bool // keys set before the file was read
	loaded map[string]bool // keys the file currently supplies
}

func loadDotenv(path string) *dotenv {
	d := &dotenv{path: path, real: map[string]bool{}, loaded: map[string]bool{}}
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		d.real[key] = true
	}
	_ = d.reload()
	return d
}

func (d *dotenv) reload() error {
	values, err := godotenv.Read(d.path)
	if errors.Is(err, fs.ErrNotExist) {
		// No file (the usual case in production) means nothing to apply.
		values, err = map[string]string{}, nil
	}
	if err != nil {
		return err
	}
	for key := range d.loaded {
		if _, ok := values[key]; !ok {
			_ = os.Unsetenv(key)
			delete(d.loaded, key)
		}
	}
	for key, value := range values {
		if d.real[key] {
			continue
		}
		if err := os.Setenv(key, value); err != nil {
			return err
		}
		d.loaded[key] = true
	}
	return nil
}
