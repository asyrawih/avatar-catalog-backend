// Package db membawa berkas SQL repo ini ke dalam biner.
//
// Migrasi diembed, bukan dibaca dari disk, supaya satu image berisi kode dan
// skema yang cocok satu sama lain: tidak mungkin pod berjalan dengan biner
// versi baru tapi berkas migrasi versi lama, dan tidak perlu menyalin db/ ke
// dalam image atau ke ConfigMap.
package db

import "embed"

// Migrations berisi seluruh isi db/migrations. Direktorinya diembed utuh
// (bukan pola *.sql) supaya embed tidak gagal saat belum ada satu migrasi pun;
// yang bukan .sql diabaikan oleh internal/migrate.
//
//go:embed migrations
var Migrations embed.FS

// MigrationsDir adalah akar berkas migrasi di dalam Migrations.
const MigrationsDir = "migrations"
