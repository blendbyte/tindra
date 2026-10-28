package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"runtime"
	"strings"

	"github.com/joho/godotenv"
)

// systemEnvFile is read when the working directory has no .env, so commands an
// admin runs by hand (sudo -u tindra tindra users create) see the same
// configuration as a service that reads it.
const systemEnvFile = "/etc/tindra/tindra.env"

// envFilePath picks the configuration file: TINDRA_ENV_FILE when set, which
// must then exist; otherwise ./.env, or the system file outside Windows when
// the working directory has none. Those two are optional.
func envFilePath(system string) (path string, required bool) {
	if path := os.Getenv("TINDRA_ENV_FILE"); path != "" {
		return path, true
	}
	if _, err := os.Stat(".env"); runtime.GOOS == "windows" || !errors.Is(err, fs.ErrNotExist) {
		return ".env", false
	}
	return system, false
}

// dotenv applies a .env file beneath the real environment: variables set by
// the process environment (Docker, systemd, the shell) always win. Reloading on
// SIGHUP keeps that order, picks up edited values, and drops removed ones.
type dotenv struct {
	path     string
	required bool            // a missing file is an error, not an empty one
	found    bool            // the last read found the file
	real     map[string]bool // keys set before the file was read
	loaded   map[string]bool // keys the file currently supplies
}

// loadDotenv fails when the file exists but cannot be read or parsed, so a
// command never runs against defaults while its configuration is unreadable.
func loadDotenv(path string, required bool) (*dotenv, error) {
	d := &dotenv{path: path, required: required, real: map[string]bool{}, loaded: map[string]bool{}}
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		d.real[key] = true
	}
	return d, d.reload()
}

func (d *dotenv) reload() error {
	values, err := godotenv.Read(d.path)
	d.found = err == nil
	if errors.Is(err, fs.ErrNotExist) && !d.required {
		// No file (the usual case in production) means nothing to apply.
		values, err = map[string]string{}, nil
	}
	if err != nil {
		// Open errors already name the file; parse errors don't.
		if pathErr := (*fs.PathError)(nil); !errors.As(err, &pathErr) {
			err = fmt.Errorf("%s: %w", d.path, err)
		}
		return fmt.Errorf("read configuration file: %w", err)
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
