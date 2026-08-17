// Package migrate menjalankan berkas SQL migrasi secara berurutan, sekali saja
// per database.
//
// Catatannya disimpan di tabel schema_migrations di database yang sama, jadi
// "sudah pernah jalan atau belum" adalah pertanyaan yang dijawab database itu
// sendiri — bukan oleh berkas penanda di server yang bisa hilang, dan bukan
// oleh ingatan orang yang men-deploy.
package migrate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// lockID mengunci seluruh proses migrasi lewat advisory lock Postgres. Dua pod
// api yang start bersamaan menjalankan runner ini pada saat yang sama; tanpa
// kunci, keduanya melihat migrasi yang sama "belum diterapkan" dan menjalankan
// DDL-nya dua kali.
//
// Angkanya sembarang, yang penting sama di semua proses.
const lockID int64 = 7_311_902_461_055

// noTxMarker menandai berkas yang tidak boleh dibungkus transaksi.
const noTxMarker = "-- migrate: no-transaction"

// Migration adalah satu berkas migrasi.
type Migration struct {
	Version  string // awalan nomor berkas, mis. "0001"
	Name     string // nama berkas tanpa ekstensi
	SQL      string
	Checksum string // sha256 isi berkas, untuk mendeteksi berkas yang diedit
	InTx     bool
}

// State adalah satu migrasi beserta status penerapannya.
type State struct {
	Migration
	AppliedAt *time.Time
	Drifted   bool // sudah diterapkan, tapi isi berkasnya kini berbeda
}

// Load membaca semua berkas .sql di dalam dir, terurut menurut nama.
func Load(fsys fs.FS, dir string) ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("membaca %s: %w", dir, err)
	}

	var out []Migration
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		raw, err := fs.ReadFile(fsys, path.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("membaca %s: %w", e.Name(), err)
		}
		body := string(raw)
		sum := sha256.Sum256(raw)
		name := strings.TrimSuffix(e.Name(), ".sql")

		version, _, ok := strings.Cut(name, "_")
		if !ok || version == "" {
			return nil, fmt.Errorf("nama berkas %q tidak berbentuk NNNN_deskripsi.sql", e.Name())
		}

		out = append(out, Migration{
			Version:  version,
			Name:     name,
			SQL:      body,
			Checksum: hex.EncodeToString(sum[:]),
			InTx:     !strings.HasPrefix(strings.TrimSpace(body), noTxMarker),
		})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	for i := 1; i < len(out); i++ {
		if out[i].Version == out[i-1].Version {
			return nil, fmt.Errorf("versi %s dipakai dua berkas: %s dan %s",
				out[i].Version, out[i-1].Name, out[i].Name)
		}
	}
	return out, nil
}

// Up menerapkan semua migrasi yang belum tercatat, berurutan, dan mengembalikan
// yang baru saja diterapkan. Aman dipanggil bersamaan dari beberapa proses.
func Up(ctx context.Context, conn *pgx.Conn, migrations []Migration) ([]Migration, error) {
	if err := lock(ctx, conn); err != nil {
		return nil, err
	}
	defer unlock(conn)

	if err := ensureTable(ctx, conn); err != nil {
		return nil, err
	}
	applied, err := appliedChecksums(ctx, conn)
	if err != nil {
		return nil, err
	}

	var done []Migration
	for _, m := range migrations {
		if sum, ok := applied[m.Version]; ok {
			if sum != m.Checksum {
				return done, driftError(m)
			}
			continue
		}
		if err := apply(ctx, conn, m); err != nil {
			return done, fmt.Errorf("migrasi %s: %w", m.Name, err)
		}
		done = append(done, m)
	}
	return done, nil
}

// Status membaca migrasi yang ada beserta catatan penerapannya. Tidak menulis
// apa pun kecuali tabel catatannya sendiri belum ada.
func Status(ctx context.Context, conn *pgx.Conn, migrations []Migration) ([]State, error) {
	if err := ensureTable(ctx, conn); err != nil {
		return nil, err
	}

	rows, err := conn.Query(ctx, `SELECT version, checksum, applied_at FROM schema_migrations`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type record struct {
		checksum string
		at       time.Time
	}
	seen := map[string]record{}
	for rows.Next() {
		var version string
		var r record
		if err := rows.Scan(&version, &r.checksum, &r.at); err != nil {
			return nil, err
		}
		seen[version] = r
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]State, 0, len(migrations))
	for _, m := range migrations {
		st := State{Migration: m}
		if r, ok := seen[m.Version]; ok {
			at := r.at
			st.AppliedAt = &at
			st.Drifted = r.checksum != m.Checksum
		}
		out = append(out, st)
	}
	return out, nil
}

// Baseline mencatat semua migrasi sebagai sudah diterapkan tanpa menjalankan
// SQL-nya. Dipakai sekali pada database yang skemanya sudah terkini — database
// baru dari db/init, atau produksi lama sebelum tabel catatan ini ada.
func Baseline(ctx context.Context, conn *pgx.Conn, migrations []Migration) ([]Migration, error) {
	if err := lock(ctx, conn); err != nil {
		return nil, err
	}
	defer unlock(conn)

	if err := ensureTable(ctx, conn); err != nil {
		return nil, err
	}
	applied, err := appliedChecksums(ctx, conn)
	if err != nil {
		return nil, err
	}

	var done []Migration
	for _, m := range migrations {
		if _, ok := applied[m.Version]; ok {
			continue
		}
		if err := record(ctx, conn, m); err != nil {
			return done, fmt.Errorf("mencatat %s: %w", m.Name, err)
		}
		done = append(done, m)
	}
	return done, nil
}

func ensureTable(ctx context.Context, conn *pgx.Conn) error {
	_, err := conn.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
		    version    text PRIMARY KEY,
		    name       text NOT NULL,
		    checksum   text NOT NULL,
		    applied_at timestamptz NOT NULL DEFAULT now()
		)`)
	if err != nil {
		return fmt.Errorf("menyiapkan tabel schema_migrations: %w", err)
	}
	return nil
}

func appliedChecksums(ctx context.Context, conn *pgx.Conn) (map[string]string, error) {
	rows, err := conn.Query(ctx, `SELECT version, checksum FROM schema_migrations`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]string{}
	for rows.Next() {
		var version, checksum string
		if err := rows.Scan(&version, &checksum); err != nil {
			return nil, err
		}
		out[version] = checksum
	}
	return out, rows.Err()
}

// apply menjalankan satu berkas dan mencatatnya dalam transaksi yang sama, jadi
// tidak ada keadaan "SQL-nya jalan tapi catatannya gagal ditulis".
func apply(ctx context.Context, conn *pgx.Conn, m Migration) error {
	if !m.InTx {
		if _, err := conn.Exec(ctx, m.SQL); err != nil {
			return err
		}
		return record(ctx, conn, m)
	}

	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))

	if _, err := tx.Exec(ctx, m.SQL); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO schema_migrations (version, name, checksum) VALUES ($1, $2, $3)`,
		m.Version, m.Name, m.Checksum,
	); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func record(ctx context.Context, conn *pgx.Conn, m Migration) error {
	_, err := conn.Exec(ctx,
		`INSERT INTO schema_migrations (version, name, checksum) VALUES ($1, $2, $3)
		 ON CONFLICT (version) DO NOTHING`,
		m.Version, m.Name, m.Checksum)
	return err
}

func lock(ctx context.Context, conn *pgx.Conn) error {
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, lockID); err != nil {
		return fmt.Errorf("mengambil advisory lock: %w", err)
	}
	return nil
}

// unlock memakai context sendiri: kalau ctx pemanggil sudah batal (mis.
// timeout), kunci tetap harus dilepas sebelum koneksi ditutup.
func unlock(conn *pgx.Conn) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = conn.Exec(ctx, `SELECT pg_advisory_unlock($1)`, lockID)
}

// ErrDrift dikembalikan saat berkas migrasi yang sudah diterapkan diedit.
var ErrDrift = errors.New("isi berkas migrasi berubah setelah diterapkan")

func driftError(m Migration) error {
	return fmt.Errorf("%w: %s sudah tercatat di schema_migrations dengan isi berbeda. "+
		"Berkas yang sudah jalan tidak boleh diedit — tulis migrasi baru. "+
		"Kalau perubahannya memang cuma komentar dan skemanya sudah benar, "+
		"perbarui checksum-nya: UPDATE schema_migrations SET checksum = '%s' WHERE version = '%s'",
		ErrDrift, m.Name, m.Checksum, m.Version)
}
