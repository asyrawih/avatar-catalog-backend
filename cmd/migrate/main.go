// Command migrate menerapkan berkas SQL di db/migrations ke database yang
// ditunjuk DATABASE_URL.
//
//	migrate up         terapkan yang belum pernah jalan (default)
//	migrate status     lihat mana yang sudah dan belum
//	migrate baseline   tandai semua sebagai sudah diterapkan, tanpa menjalankan
//
// Berkas migrasinya diembed ke dalam biner ini, jadi perintahnya bisa
// dijalankan dari mana saja — termasuk dari dalam pod api yang tidak membawa
// checkout repo.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/hanan/avatar-catalog-backend/db"
	"github.com/hanan/avatar-catalog-backend/internal/migrate"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	cmd := "up"
	if len(args) > 0 {
		cmd = args[0]
	}

	switch cmd {
	case "-h", "--help", "help":
		usage()
		return nil
	case "up", "status", "baseline":
	default:
		usage()
		return fmt.Errorf("perintah %q tidak dikenal", cmd)
	}

	migrations, err := migrate.Load(db.Migrations, db.MigrationsDir)
	if err != nil {
		return err
	}

	// Longgar: satu migrasi pada tabel besar bisa lama, dan initContainer yang
	// menyerah di tengah DDL meninggalkan kebingungan yang lebih mahal daripada
	// menunggu.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	conn, err := connect(ctx)
	if err != nil {
		return err
	}
	defer conn.Close(context.WithoutCancel(ctx))

	switch cmd {
	case "up":
		return up(ctx, conn, migrations)
	case "status":
		return status(ctx, conn, migrations)
	default:
		return baseline(ctx, conn, migrations)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `migrate — terapkan perubahan skema database

  migrate up         terapkan migrasi yang belum pernah jalan (default)
  migrate status     lihat mana yang sudah dan belum diterapkan
  migrate baseline   tandai semua migrasi sebagai sudah diterapkan tanpa
                     menjalankannya — untuk database yang skemanya sudah
                     terkini (database baru dari db/init, atau produksi lama)

DATABASE_URL wajib diisi.
`)
}

func connect(ctx context.Context) (*pgx.Conn, error) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return nil, errors.New("DATABASE_URL wajib diisi")
	}

	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("DATABASE_URL tidak sah: %w", err)
	}
	// Protokol sederhana: satu berkas migrasi berisi banyak perintah sekaligus,
	// dan protokol extended (bawaan pgx) menolak lebih dari satu perintah per
	// Exec. Argumen $1 tetap aman — pgx yang meng-escape-nya di sisi klien.
	cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol

	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("menghubungi database: %w", err)
	}
	return conn, nil
}

func up(ctx context.Context, conn *pgx.Conn, migrations []migrate.Migration) error {
	applied, err := migrate.Up(ctx, conn, migrations)
	for _, m := range applied {
		fmt.Printf("diterapkan: %s\n", m.Name)
	}
	if err != nil {
		return err
	}
	if len(applied) == 0 {
		fmt.Printf("skema sudah terkini (%d migrasi tercatat)\n", len(migrations))
	}
	return nil
}

func status(ctx context.Context, conn *pgx.Conn, migrations []migrate.Migration) error {
	states, err := migrate.Status(ctx, conn, migrations)
	if err != nil {
		return err
	}
	if len(states) == 0 {
		fmt.Println("belum ada berkas migrasi di db/migrations")
		return nil
	}

	pending := 0
	for _, st := range states {
		switch {
		case st.AppliedAt == nil:
			pending++
			fmt.Printf("belum   %s\n", st.Name)
		case st.Drifted:
			fmt.Printf("DRIFT   %s (diterapkan %s, isi berkas kini berbeda)\n",
				st.Name, st.AppliedAt.Format(time.RFC3339))
		default:
			fmt.Printf("sudah   %s (%s)\n", st.Name, st.AppliedAt.Format(time.RFC3339))
		}
	}
	fmt.Printf("\n%d belum diterapkan\n", pending)
	return nil
}

func baseline(ctx context.Context, conn *pgx.Conn, migrations []migrate.Migration) error {
	marked, err := migrate.Baseline(ctx, conn, migrations)
	if err != nil {
		return err
	}
	for _, m := range marked {
		fmt.Printf("ditandai sudah diterapkan: %s\n", m.Name)
	}
	if len(marked) == 0 {
		fmt.Println("tidak ada yang perlu ditandai")
	}
	return nil
}
