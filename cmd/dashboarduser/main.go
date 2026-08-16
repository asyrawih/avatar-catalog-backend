// Command dashboarduser mengelola operator yang bisa login ke dashboard.
//
//	dashboarduser create --email tim@contoh.com --name "Tim Ops"
//	dashboarduser list
//	dashboarduser disable <userId>
//	dashboarduser enable <userId>
//	dashboarduser passwd <userId>
//
// Kata sandi tidak pernah diterima lewat argumen: argumen ikut tercatat di
// riwayat shell dan terlihat di daftar proses mesin yang sama. Perintah di sini
// menanyakannya lewat stdin, atau membacanya dari env DASHBOARD_PASSWORD untuk
// pemakaian di skrip.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/hanan/avatar-catalog-backend/internal/auth"
	"github.com/hanan/avatar-catalog-backend/internal/service"
	"github.com/hanan/avatar-catalog-backend/internal/store"
	"github.com/hanan/avatar-catalog-backend/internal/store/postgres"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		usage()
		return errors.New("perintah tidak diberikan")
	}

	switch args[0] {
	case "create":
		return create(args[1:])
	case "list":
		return list()
	case "disable":
		return setDisabled(args[1:], true)
	case "enable":
		return setDisabled(args[1:], false)
	case "passwd":
		return passwd(args[1:])
	case "-h", "--help", "help":
		usage()
		return nil
	default:
		usage()
		return fmt.Errorf("perintah %q tidak dikenal", args[0])
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `dashboarduser — kelola operator dashboard avatar-catalog

  dashboarduser create --email <email> [--name <nama>]
  dashboarduser list
  dashboarduser disable <userId>
  dashboarduser enable <userId>
  dashboarduser passwd <userId>

Kata sandi ditanyakan lewat stdin, atau diambil dari DASHBOARD_PASSWORD.
Minimal %d karakter. DATABASE_URL wajib diisi.
`, auth.MinPasswordLen)
}

func create(args []string) error {
	fs := flag.NewFlagSet("create", flag.ContinueOnError)
	email := fs.String("email", "", "email operator, sekaligus nama login")
	name := fs.String("name", "", "nama yang ditampilkan; kosong = pakai email")
	if err := fs.Parse(args); err != nil {
		return err
	}

	password, err := readPassword()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	users, closeStore, err := openStore(ctx)
	if err != nil {
		return err
	}
	defer closeStore()

	user, err := service.NewDashboardAuth(users).CreateUser(ctx, service.CreateUserInput{
		Email:    *email,
		Name:     *name,
		Password: password,
	})
	if err != nil {
		return err
	}

	fmt.Printf("operator dibuat\n\n  userId : %s\n  email  : %s\n  nama   : %s\n\n",
		user.UserID, user.Email, user.Name)
	fmt.Print("Sesi login berlaku 8 jam dan dibawa lewat cookie httpOnly.\n" +
		"Operator ini memegang SELURUH scope — termasuk menerbitkan kunci API.\n")
	return nil
}

func list() error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	users, closeStore, err := openStore(ctx)
	if err != nil {
		return err
	}
	defer closeStore()

	rows, err := users.List(ctx)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		fmt.Println("belum ada operator. Buat dengan: dashboarduser create --email you@contoh.com")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "USER ID\tEMAIL\tNAMA\tSTATUS\tLOGIN TERAKHIR")
	for _, user := range rows {
		status := "aktif"
		if !user.Active() {
			status = "nonaktif"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			user.UserID, user.Email, user.Name, status, timeText(user.LastLoginAt))
	}
	return w.Flush()
}

func setDisabled(args []string, disabled bool) error {
	if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
		return errors.New("pemakaian: dashboarduser disable|enable <userId>")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	users, closeStore, err := openStore(ctx)
	if err != nil {
		return err
	}
	defer closeStore()

	if err := service.NewDashboardAuth(users).SetDisabled(ctx, args[0], disabled); err != nil {
		return err
	}
	if disabled {
		fmt.Printf("operator %s dinonaktifkan; seluruh sesinya ikut dimatikan\n", args[0])
		return nil
	}
	fmt.Printf("operator %s diaktifkan kembali; ia perlu login lagi\n", args[0])
	return nil
}

func passwd(args []string) error {
	if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
		return errors.New("pemakaian: dashboarduser passwd <userId>")
	}

	password, err := readPassword()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	users, closeStore, err := openStore(ctx)
	if err != nil {
		return err
	}
	defer closeStore()

	if err := service.NewDashboardAuth(users).ChangePassword(ctx, args[0], password); err != nil {
		return err
	}
	fmt.Printf("kata sandi %s diganti; seluruh sesi lamanya dimatikan\n", args[0])
	return nil
}

// readPassword mengambil kata sandi dari DASHBOARD_PASSWORD atau stdin.
//
// Tidak memakai flag: nilai flag terlihat di `ps` dan tersimpan di riwayat
// shell, dan kata sandi yang bocor lewat dua tempat itu tidak akan pernah
// disadari pemiliknya.
func readPassword() (string, error) {
	if fromEnv := os.Getenv("DASHBOARD_PASSWORD"); fromEnv != "" {
		return fromEnv, nil
	}

	fmt.Fprintf(os.Stderr, "kata sandi (minimal %d karakter): ", auth.MinPasswordLen)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", errors.New("kata sandi tidak terbaca; isi DASHBOARD_PASSWORD kalau menjalankannya dari skrip")
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func timeText(at *time.Time) string {
	if at == nil {
		return "belum pernah"
	}
	return at.Local().Format(time.RFC3339)
}

// openStore membuka penyimpanan operator dari DATABASE_URL.
func openStore(ctx context.Context) (store.DashboardUsers, func(), error) {
	dsn := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if dsn == "" {
		return nil, nil, errors.New("DATABASE_URL belum diisi; operator dashboard disimpan di Postgres")
	}

	pool, err := postgres.Open(ctx, dsn, postgres.PoolConfig{MaxConns: 2, ConnectTimeout: 10 * time.Second})
	if err != nil {
		return nil, nil, err
	}
	return postgres.NewDashboardUsers(pool), pool.Close, nil
}
