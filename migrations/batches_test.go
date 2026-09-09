package migrations

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4/database"
)

type recordingDriver struct {
	database.Driver
	calls  []string
	failAt int
}

func (d *recordingDriver) Run(r io.Reader) error {
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	d.calls = append(d.calls, string(b))
	if len(d.calls) == d.failAt {
		return errors.New("index build failed")
	}
	return nil
}

func TestBatchedDriverStopsOnFailure(t *testing.T) {
	d := &recordingDriver{failAt: 2}
	err := (BatchedDriver{Driver: d}).Run(strings.NewReader("first\n-- tindra:next-batch\nsecond\n-- tindra:next-batch\nthird"))
	if err == nil || !strings.Contains(err.Error(), "batch 2") || len(d.calls) != 2 {
		t.Fatalf("calls=%v err=%v", d.calls, err)
	}
}

func TestUsageMigrationRemainsOneBatch(t *testing.T) {
	sql, err := FS.ReadFile("0020_telemetry_usage.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	d := &recordingDriver{}
	if err := (BatchedDriver{Driver: d}).Run(strings.NewReader(string(sql))); err != nil {
		t.Fatal(err)
	}
	if len(d.calls) != 1 || d.calls[0] != string(sql) {
		t.Fatal("transactional function migration was split or modified")
	}
}
