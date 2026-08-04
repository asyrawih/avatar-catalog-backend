// Package paging membungkus cursor paginasi.
//
// Katalog berubah terus karena sync worker, jadi offset akan menggeser hasil
// di antara dua permintaan. Cursor dikodekan base64url tanpa padding supaya
// aman ditempel di query string dan tetap buram bagi klien.
package paging

import (
	"encoding/base64"
	"encoding/json"
	"time"
)

// KeysetCursor menandai baris terakhir pada daftar yang diurutkan waktu
// menurun, dengan id sebagai pemecah seri. Dipakai daftar outfit (updatedAt)
// maupun daftar transaksi (occurredAt).
type KeysetCursor struct {
	At time.Time `json:"at"`
	ID string    `json:"id"`
}

// After melaporkan apakah baris (at, id) berada setelah posisi cursor pada
// urutan menurun.
func (c KeysetCursor) After(at time.Time, id string) bool {
	if at.Equal(c.At) {
		return id > c.ID
	}
	return at.Before(c.At)
}

// PositionCursor menandai posisi absolut pada daftar yang urutannya stabil,
// mis. isi etalase yang diurutkan kolom position.
type PositionCursor struct {
	Pos int `json:"pos"`
}

// Encode mengubah cursor apa pun menjadi string buram.
func Encode(v any) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// Decode membaca cursor buram ke dalam dst. Cursor kosong berarti halaman
// pertama, dan bukan error.
func Decode(raw string, dst any) (bool, error) {
	if raw == "" {
		return false, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return false, err
	}
	if err := json.Unmarshal(decoded, dst); err != nil {
		return false, err
	}
	return true, nil
}

// ClampLimit menjaga limit tetap di rentang wajar.
func ClampLimit(limit, fallback, max int) int {
	switch {
	case limit <= 0:
		return fallback
	case limit > max:
		return max
	default:
		return limit
	}
}
