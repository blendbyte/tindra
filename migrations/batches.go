package migrations

import (
	"fmt"
	"io"
	"strings"

	"github.com/golang-migrate/migrate/v4/database"
)

// Batches splits only at our explicit, standalone batch directive. Ordinary
// migrations, including dollar-quoted functions and transactions, stay intact.
// The reserved directive must never appear inside SQL strings or function bodies.
func Batches(sql string) []string {
	return strings.Split(sql, "\n-- tindra:next-batch\n")
}

// BatchedDriver preserves the underlying driver's locking and version tracking,
// but submits explicitly separated batches individually, outside a transaction.
type BatchedDriver struct {
	database.Driver
}

func (d BatchedDriver) Run(r io.Reader) error {
	sql, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	for i, batch := range Batches(string(sql)) {
		if err := d.Driver.Run(strings.NewReader(batch)); err != nil {
			return fmt.Errorf("migration batch %d: %w", i+1, err)
		}
	}
	return nil
}
