// Package service berisi aturan bisnis di atas store. Lapisan ini tidak tahu
// apa-apa soal HTTP; kegagalan dikembalikan sebagai *apierr.Error supaya
// pemetaan status kode tetap satu tempat.
package service

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

// randomHex mengembalikan n byte acak dalam heksadesimal.
func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand tidak pernah gagal di platform yang didukung; panic di
		// sini lebih baik daripada diam-diam mencetak id yang bertabrakan.
		panic(err)
	}
	return hex.EncodeToString(b)
}

// newOutfitID mencetak id outfit bergaya otf_9f2a41.
func newOutfitID() string { return "otf_" + randomHex(3) }

// newTxID mencetak id transaksi bergaya tx_44c1a9.
func newTxID() string { return "tx_" + randomHex(3) }

// newRunID mencetak id sync run bergaya run_20260804_0200 dari waktu mulai.
func newRunID(startedAt time.Time) string {
	return "run_" + startedAt.UTC().Format("20060102_1504")
}

// newUUID mencetak UUID v4. Nilai inilah yang dipakai sebagai referenceId dan
// dikirim ke RecommendationService:RegisterItemAsync.
func newUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 0x0f) | 0x40 // versi 4
	b[8] = (b[8] & 0x3f) | 0x80 // varian RFC 4122
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
