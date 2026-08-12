// Command apikey menerbitkan, melihat, dan mencabut kunci API.
//
//	apikey issue --name roblox-game-server-prod --role game-server --expires 90d
//	apikey issue --name dashboard --role dashboard
//	apikey issue --name eksperimen --scopes catalog:read,transactions:read
//	apikey list
//	apikey revoke <keyId>
//	apikey roles
//
// Token utuh hanya ditampilkan sekali, saat diterbitkan. Yang tersimpan di
// database cuma hash-nya, jadi token yang hilang tidak bisa dipulihkan — hanya
// bisa diterbitkan ulang.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/hanan/avatar-catalog-backend/internal/auth"
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
	case "issue":
		return issue(args[1:])
	case "list":
		return list()
	case "revoke":
		return revoke(args[1:])
	case "roles":
		return printRoles()
	case "-h", "--help", "help":
		usage()
		return nil
	default:
		usage()
		return fmt.Errorf("perintah %q tidak dikenal", args[0])
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `apikey — kelola kunci API avatar-catalog

  apikey issue --name <nama> (--role <role> | --scopes a,b) [--expires 90d] [--env live|test]
  apikey list
  apikey revoke <keyId>
  apikey roles

DATABASE_URL wajib diisi.
`)
}

func issue(args []string) error {
	fs := flag.NewFlagSet("issue", flag.ContinueOnError)
	name := fs.String("name", "", "nama kunci yang bisa dibaca manusia, mis. roblox-game-server-prod")
	role := fs.String("role", "", "paket scope siap pakai (lihat: apikey roles)")
	scopeList := fs.String("scopes", "", "daftar scope dipisah koma, sebagai ganti --role")
	expires := fs.String("expires", "", "masa berlaku, mis. 90d atau 720h; kosong = tanpa batas")
	env := fs.String("env", auth.EnvLive, "live atau test")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if strings.TrimSpace(*name) == "" {
		return errors.New("--name wajib diisi; nama ini yang muncul di log saat kunci dipakai")
	}
	scopes, err := resolveScopes(*role, *scopeList)
	if err != nil {
		return err
	}
	expiresAt, err := parseExpiry(*expires)
	if err != nil {
		return err
	}
	if expiresAt == nil {
		fmt.Fprintln(os.Stderr, "catatan: kunci ini tanpa masa berlaku. Kunci untuk pihak luar sebaiknya diberi --expires.")
	}

	token, err := auth.Generate(*env)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	keys, closeStore, err := openStore(ctx)
	if err != nil {
		return err
	}
	defer closeStore()

	key := auth.Key{
		KeyID:     token.KeyID,
		Hash:      token.Hash,
		Name:      strings.TrimSpace(*name),
		Scopes:    scopes,
		CreatedAt: time.Now().UTC(),
		ExpiresAt: expiresAt,
	}
	if err := keys.Create(ctx, key); err != nil {
		return err
	}

	fmt.Printf("kunci diterbitkan\n\n")
	fmt.Printf("  keyId   : %s\n", key.KeyID)
	fmt.Printf("  nama    : %s\n", key.Name)
	fmt.Printf("  scope   : %s\n", strings.Join(auth.ScopeStrings(key.Scopes), ", "))
	fmt.Printf("  berlaku : %s\n\n", expiryText(expiresAt))
	fmt.Printf("  token   : %s\n\n", token.Secret)
	fmt.Print("Simpan token itu sekarang — hanya hash-nya yang masuk database dan\n" +
		"nilainya tidak bisa ditampilkan lagi. Untuk Roblox, simpan sebagai Secret\n" +
		"di Creator Hub, bukan sebagai string di dalam Script.\n")
	return nil
}

func list() error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	keys, closeStore, err := openStore(ctx)
	if err != nil {
		return err
	}
	defer closeStore()

	rows, err := keys.List(ctx)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		fmt.Println("belum ada kunci. Terbitkan dengan: apikey issue --name <nama> --role game-server")
		return nil
	}

	now := time.Now().UTC()
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "KEY ID\tNAMA\tSTATUS\tDIPAKAI TERAKHIR\tSCOPE")
	for _, key := range rows {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			key.KeyID, key.Name, statusText(key, now), timeText(key.LastUsedAt),
			strings.Join(auth.ScopeStrings(key.Scopes), ","))
	}
	return w.Flush()
}

func revoke(args []string) error {
	if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
		return errors.New("pemakaian: apikey revoke <keyId>")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	keys, closeStore, err := openStore(ctx)
	if err != nil {
		return err
	}
	defer closeStore()

	if err := keys.Revoke(ctx, args[0], time.Now().UTC()); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("keyId %q tidak ada", args[0])
		}
		return err
	}
	fmt.Printf("kunci %s dicabut; berlaku pada request berikutnya\n", args[0])
	return nil
}

func printRoles() error {
	names := make([]string, 0, len(auth.Roles))
	for name := range auth.Roles {
		names = append(names, name)
	}
	slices.Sort(names)

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ROLE\tSCOPE")
	for _, name := range names {
		fmt.Fprintf(w, "%s\t%s\n", name, strings.Join(auth.ScopeStrings(auth.Roles[name]), ","))
	}
	return w.Flush()
}

// resolveScopes menerjemahkan --role atau --scopes menjadi daftar scope.
func resolveScopes(role, scopeList string) ([]auth.Scope, error) {
	role, scopeList = strings.TrimSpace(role), strings.TrimSpace(scopeList)
	switch {
	case role != "" && scopeList != "":
		return nil, errors.New("pilih salah satu: --role atau --scopes")
	case role != "":
		scopes, ok := auth.Roles[role]
		if !ok {
			return nil, fmt.Errorf("role %q tidak dikenal; lihat: apikey roles", role)
		}
		return scopes, nil
	case scopeList != "":
		return auth.ParseScopes(strings.Split(scopeList, ","))
	default:
		return nil, errors.New("isi --role atau --scopes; kunci tanpa scope tidak berguna")
	}
}

// parseExpiry menerima "90d" selain format durasi Go, karena masa berlaku kunci
// hampir selalu dinyatakan dalam hari.
func parseExpiry(raw string) (*time.Time, error) {
	if raw = strings.TrimSpace(raw); raw == "" {
		return nil, nil
	}

	dur, err := time.ParseDuration(raw)
	if err != nil {
		days, dayErr := strconv.Atoi(strings.TrimSuffix(raw, "d"))
		if dayErr != nil || !strings.HasSuffix(raw, "d") || days <= 0 {
			return nil, fmt.Errorf("--expires %q tidak bisa dibaca; contoh: 90d atau 720h", raw)
		}
		dur = time.Duration(days) * 24 * time.Hour
	}
	if dur <= 0 {
		return nil, errors.New("--expires harus positif")
	}
	at := time.Now().UTC().Add(dur)
	return &at, nil
}

// openStore membuka penyimpanan kunci dari DATABASE_URL.
func openStore(ctx context.Context) (store.APIKeys, func(), error) {
	dsn := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if dsn == "" {
		return nil, nil, errors.New("DATABASE_URL belum diisi; kunci API disimpan di Postgres")
	}

	pool, err := postgres.Open(ctx, dsn, postgres.PoolConfig{MaxConns: 2, ConnectTimeout: 10 * time.Second})
	if err != nil {
		return nil, nil, err
	}
	return postgres.NewAPIKeys(pool), pool.Close, nil
}

func statusText(key auth.Key, now time.Time) string {
	switch {
	case key.RevokedAt != nil:
		return "dicabut " + key.RevokedAt.Format(time.DateOnly)
	case key.ExpiresAt != nil && !now.Before(*key.ExpiresAt):
		return "kedaluwarsa " + key.ExpiresAt.Format(time.DateOnly)
	case key.ExpiresAt != nil:
		return "aktif s/d " + key.ExpiresAt.Format(time.DateOnly)
	default:
		return "aktif"
	}
}

func expiryText(at *time.Time) string {
	if at == nil {
		return "tanpa batas"
	}
	return at.Format(time.RFC3339)
}

func timeText(at *time.Time) string {
	if at == nil {
		return "belum pernah"
	}
	return at.Format(time.RFC3339)
}
