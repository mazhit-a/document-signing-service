package postgres

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

func newMigrator(databaseURL string, files fs.FS) (*migrate.Migrate, error) {
	src, err := iofs.New(files, ".")
	if err != nil {
		return nil, fmt.Errorf("migration source: %w", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, toPgx5URL(databaseURL))
	if err != nil {
		return nil, fmt.Errorf("migrator: %w", err)
	}
	return m, nil
}

func toPgx5URL(u string) string {
	switch {
	case strings.HasPrefix(u, "postgres://"):
		return "pgx5://" + strings.TrimPrefix(u, "postgres://")
	case strings.HasPrefix(u, "postgresql://"):
		return "pgx5://" + strings.TrimPrefix(u, "postgresql://")
	}
	return u
}

func run(databaseURL string, files fs.FS, step func(*migrate.Migrate) error) error {
	m, err := newMigrator(databaseURL, files)
	if err != nil {
		return err
	}
	defer m.Close()
	if err := step(m); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return err
	}
	return nil
}

func MigrateUp(databaseURL string, files fs.FS) error {
	return run(databaseURL, files, func(m *migrate.Migrate) error { return m.Up() })
}

func MigrateDown(databaseURL string, files fs.FS) error {
	return run(databaseURL, files, func(m *migrate.Migrate) error { return m.Down() })
}
