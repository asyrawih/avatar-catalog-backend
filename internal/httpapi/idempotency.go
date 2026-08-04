package httpapi

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/hanan/avatar-catalog-backend/internal/apierr"
	"github.com/hanan/avatar-catalog-backend/internal/idempotency"
)

// idempotencyHeader adalah nama header yang dibangkitkan klien sebelum
// permintaan pertama.
const idempotencyHeader = "Idempotency-Key"

// idempotent menyimpan respons sukses per Idempotency-Key lalu memutarnya
// kembali saat kunci yang sama datang lagi.
//
// Dipakai untuk POST yang membuat baris baru. POST transaksi tidak lewat sini:
// idempotensinya ditegakkan kolom unik di tabel TRANSACTION, bukan cache
// respons, sehingga tetap berlaku walau proses backend sempat restart.
func idempotent(store idempotency.Store, scope string, required bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := r.Header.Get(idempotencyHeader)
			if key == "" {
				if required {
					writeError(w, apierr.BadRequest("missing_idempotency_key", "Header Idempotency-Key wajib untuk endpoint ini"))
					return
				}
				next.ServeHTTP(w, r)
				return
			}

			if rec, ok := store.Get(scope, key); ok {
				writeReplay(w, rec)
				return
			}

			rec := &captureWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)

			if rec.status >= 200 && rec.status < 300 {
				store.Put(scope, key, idempotency.Record{Status: rec.status, Body: rec.body.Bytes()})
			}
		})
	}
}

// writeReplay menulis ulang respons tersimpan dan menandainya sebagai
// pengulangan, supaya klien tahu tidak ada baris baru yang dibuat.
func writeReplay(w http.ResponseWriter, rec idempotency.Record) {
	var payload map[string]any
	if err := json.Unmarshal(rec.Body, &payload); err != nil {
		// Body tersimpan bukan objek JSON — kembalikan apa adanya.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(rec.Status)
		if _, err := w.Write(rec.Body); err != nil {
			slog.Error("gagal menulis replay idempotensi", "err", err)
		}
		return
	}

	payload["idempotentReplay"] = true
	writeJSON(w, http.StatusOK, payload)
}

// captureWriter merekam status dan body supaya bisa disimpan sebagai hasil
// idempoten, sambil tetap meneruskannya ke klien.
type captureWriter struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer
}

func (c *captureWriter) WriteHeader(status int) {
	c.status = status
	c.ResponseWriter.WriteHeader(status)
}

func (c *captureWriter) Write(b []byte) (int, error) {
	c.body.Write(b)
	return c.ResponseWriter.Write(b)
}
