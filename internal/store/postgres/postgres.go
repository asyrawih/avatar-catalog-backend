// Package postgres mengimplementasikan port di package store di atas
// Postgres, memakai pgx.
//
// Konvensi di package ini:
//   - kolom uuid dan enum di-cast eksplisit di SQL supaya pemetaan tipe tidak
//     bergantung pada tebakan driver
//   - baris tidak ditemukan diterjemahkan menjadi store.ErrNotFound
//   - penulisan yang menyentuh lebih dari satu tabel selalu dalam satu transaksi
package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PoolConfig menyetel ukuran connection pool.
type PoolConfig struct {
	MaxConns        int32
	MinConns        int32
	MaxConnLifetime time.Duration
	MaxConnIdleTime time.Duration
	ConnectTimeout  time.Duration
}

// Open membuka connection pool dan memastikan database benar-benar bisa
// dihubungi sebelum server mulai melayani.
func Open(ctx context.Context, dsn string, cfg PoolConfig) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("postgres: DSN tidak valid: %w", err)
	}

	if cfg.MaxConns > 0 {
		poolCfg.MaxConns = cfg.MaxConns
	}
	if cfg.MinConns > 0 {
		poolCfg.MinConns = cfg.MinConns
	}
	if cfg.MaxConnLifetime > 0 {
		poolCfg.MaxConnLifetime = cfg.MaxConnLifetime
	}
	if cfg.MaxConnIdleTime > 0 {
		poolCfg.MaxConnIdleTime = cfg.MaxConnIdleTime
	}
	if cfg.ConnectTimeout > 0 {
		poolCfg.ConnConfig.ConnectTimeout = cfg.ConnectTimeout
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("postgres: gagal membuat pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres: gagal terhubung: %w", err)
	}
	return pool, nil
}
