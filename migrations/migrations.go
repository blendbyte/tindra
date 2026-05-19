package migrations

import (
	"embed"
	"io/fs"
	"strings"
)

//go:embed *.sql
var FS embed.FS

// Files returns the names of all *.sql migration files in lexical order.
func Files() ([]string, error) {
	entries, err := fs.ReadDir(FS, ".")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	return names, nil
}
