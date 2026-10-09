// Command migrate applies the SQL migrations in database/migrations.
//
// It is a deliberately small, dependency-free replacement for the
// golang-migrate CLI so that the project can be set up on a machine (or CI
// sandbox) that cannot download extra tooling. It keeps the same
// `schema_migrations (version, dirty)` bookkeeping table, so switching to
// golang-migrate later requires no changes to the database.
//
// Usage:
//
//	go run ./cmd/migrate up               # apply all pending migrations
//	go run ./cmd/migrate down             # roll back the most recent migration
//	go run ./cmd/migrate version          # print the current version
//	go run ./cmd/migrate force 7          # clear a dirty state at version 7
//	go run ./cmd/migrate drop             # drop every object (dev only)
//
// The database URL is read from DATABASE_URL unless -url is given.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/sih26234/food-waste/internal/config"
)

type migration struct {
	version int64
	name    string
	up      string
	down    string
}

var fileRe = regexp.MustCompile(`^(\d+)_(.+)\.(up|down)\.sql$`)

func main() {
	config.LoadDotEnv(".env")
	dir := flag.String("dir", "database/migrations", "directory containing *.up.sql / *.down.sql migrations")
	url := flag.String("url", os.Getenv("DATABASE_URL"), "postgres connection string (defaults to $DATABASE_URL)")
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 || *url == "" {
		fmt.Fprintln(os.Stderr, "usage: migrate [-dir DIR] [-url DATABASE_URL] up|down|version|force <v>|drop")
		if *url == "" {
			fmt.Fprintln(os.Stderr, "error: DATABASE_URL is empty")
		}
		os.Exit(2)
	}

	migrations, err := load(*dir)
	if err != nil {
		log.Fatalf("load migrations: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	conn, err := pgx.Connect(ctx, *url)
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	if err := ensureTable(ctx, conn); err != nil {
		log.Fatalf("schema_migrations: %v", err)
	}

	switch args[0] {
	case "up":
		cur, dirty, _ := current(ctx, conn)
		if dirty {
			log.Fatalf("database is dirty at version %d — fix the schema and run `force %d`", cur, cur)
		}
		applied := 0
		for _, m := range migrations {
			if m.version <= cur {
				continue
			}
			if err := apply(ctx, conn, m); err != nil {
				markDirty(ctx, conn, m.version)
				log.Fatalf("migration %d_%s failed: %v", m.version, m.name, err)
			}
			applied++
		}
		if applied == 0 {
			fmt.Printf("already up to date (version %d)\n", cur)
		} else {
			cur, _, _ = current(ctx, conn)
			fmt.Printf("applied %d migration(s); now at version %d\n", applied, cur)
		}

	case "down":
		cur, _, _ := current(ctx, conn)
		var target *migration
		for i := range migrations {
			if migrations[i].version == cur {
				target = &migrations[i]
			}
		}
		if target == nil {
			fmt.Println("nothing to roll back")
			return
		}
		if err := rollback(ctx, conn, *target); err != nil {
			log.Fatalf("rollback %d_%s failed: %v", target.version, target.name, err)
		}
		fmt.Printf("rolled back %d_%s\n", target.version, target.name)

	case "version":
		cur, dirty, _ := current(ctx, conn)
		fmt.Printf("version=%d dirty=%t\n", cur, dirty)

	case "force":
		if len(args) < 2 {
			log.Fatal("force requires a version argument")
		}
		v, err := strconv.ParseInt(args[1], 10, 64)
		if err != nil {
			log.Fatalf("invalid version %q", args[1])
		}
		if _, err := conn.Exec(ctx, `UPDATE schema_migrations SET version=$1, dirty=false`, v); err != nil {
			log.Fatalf("force: %v", err)
		}
		fmt.Printf("forced version=%d dirty=false\n", v)

	case "drop":
		if err := dropAll(ctx, conn); err != nil {
			log.Fatalf("drop: %v", err)
		}
		fmt.Println("dropped all objects")

	default:
		log.Fatalf("unknown command %q", args[0])
	}
}

func load(dir string) ([]migration, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	byVersion := map[int64]*migration{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		mm := fileRe.FindStringSubmatch(e.Name())
		if mm == nil {
			continue
		}
		v, err := strconv.ParseInt(mm[1], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("bad version in %s", e.Name())
		}
		body, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		m := byVersion[v]
		if m == nil {
			m = &migration{version: v, name: mm[2]}
			byVersion[v] = m
		}
		if mm[3] == "up" {
			m.up = string(body)
		} else {
			m.down = string(body)
		}
	}
	out := make([]migration, 0, len(byVersion))
	for _, m := range byVersion {
		if m.up == "" {
			return nil, fmt.Errorf("migration %d_%s has no .up.sql", m.version, m.name)
		}
		out = append(out, *m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	return out, nil
}

func ensureTable(ctx context.Context, conn *pgx.Conn) error {
	if _, err := conn.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version bigint NOT NULL PRIMARY KEY,
			dirty   boolean NOT NULL DEFAULT false
		)`); err != nil {
		return err
	}
	var n int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		_, err := conn.Exec(ctx, `INSERT INTO schema_migrations (version, dirty) VALUES (0, false)`)
		return err
	}
	return nil
}

func current(ctx context.Context, conn *pgx.Conn) (int64, bool, error) {
	var v int64
	var dirty bool
	err := conn.QueryRow(ctx, `SELECT version, dirty FROM schema_migrations LIMIT 1`).Scan(&v, &dirty)
	if err == pgx.ErrNoRows {
		return 0, false, nil
	}
	return v, dirty, err
}

// apply runs one migration inside a transaction, then records the version.
// Note: statements that cannot run inside a transaction (e.g. CREATE INDEX
// CONCURRENTLY) are not used by this project's migrations.
func apply(ctx context.Context, conn *pgx.Conn, m migration) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	start := time.Now()
	if _, err := tx.Exec(ctx, m.up); err != nil {
		return fmt.Errorf("%w\n--- sql ---\n%s", err, strings.TrimSpace(m.up))
	}
	if _, err := tx.Exec(ctx, `UPDATE schema_migrations SET version=$1, dirty=false`, m.version); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	fmt.Printf("applied %04d_%s (%.0fms)\n", m.version, m.name, time.Since(start).Seconds()*1000)
	return nil
}

func rollback(ctx context.Context, conn *pgx.Conn, m migration) error {
	if strings.TrimSpace(m.down) == "" {
		return fmt.Errorf("migration %04d_%s has no .down.sql", m.version, m.name)
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, m.down); err != nil {
		return fmt.Errorf("%w\n--- sql ---\n%s", err, strings.TrimSpace(m.down))
	}
	// The new current version is the greatest applied version below this one.
	var prevVersion int64
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(MAX(version), 0) FROM schema_migrations WHERE version < $1`, m.version).Scan(&prevVersion); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE schema_migrations SET version=$1, dirty=false`, prevVersion); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func markDirty(ctx context.Context, conn *pgx.Conn, version int64) {
	_, _ = conn.Exec(ctx, `UPDATE schema_migrations SET version=$1, dirty=true`, version)
}

// dropAll removes everything in the public schema. Development only.
func dropAll(ctx context.Context, conn *pgx.Conn) error {
	_, err := conn.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public;`)
	return err
}
