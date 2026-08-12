package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/hanan/avatar-catalog-backend/internal/store"
)

// Embedder mengubah teks menjadi vector untuk pencarian makna lintas bahasa.
//
// Ini pintu colok, bukan keharusan: tanpa embedder pencarian tetap jalan lewat
// jalur trigram, dan skema (OUTFIT.name_embedding, index HNSW) serta query
// hybrid sudah menunggu. Implementasinya bebas — API (OpenAI, Voyage, ...)
// maupun sidecar self-host (TEI) — asal dimensinya konsisten dengan kolom
// name_embedding; ganti model berarti re-embed semua baris.
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

// embedQueryTimeout membatasi tunggu embedding saat pencarian. Semantik itu
// peningkatan, bukan ketergantungan: lebih baik jatuh ke leksikal saja daripada
// pencarian ikut lambat karena penyedia embedding sedang seret.
const embedQueryTimeout = 800 * time.Millisecond

// embedWriteTimeout membatasi embedding asinkron setelah create/rename; lebih
// longgar karena tidak ada request yang menunggu.
const embedWriteTimeout = 10 * time.Second

// queryEmbedding mengembalikan embedding kata kunci, atau nil bila embedder
// tidak terpasang / gagal — pencarian lanjut leksikal saja.
func (s *Outfits) queryEmbedding(ctx context.Context, keyword string) []float32 {
	if s.embedder == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, embedQueryTimeout)
	defer cancel()

	embedding, err := s.embedder.Embed(ctx, keyword)
	if err != nil {
		slog.Warn("gagal meng-embed kata kunci; pencarian lanjut leksikal saja", "err", err)
		return nil
	}
	return embedding
}

// maxConcurrentEmbeds memplafon jumlah embedding yang berjalan bersamaan.
// Delapan: cukup untuk mengejar laju create normal, dan tetap jauh di bawah
// DB_MAX_CONNS (bawaan 10) sehingga kerja latar tidak pernah menghabiskan pool
// koneksi milik request yang sedang ditunggu klien.
const maxConcurrentEmbeds = 8

// embedNameAsync meng-embed nama outfit di belakang layar lalu menyimpannya.
// Dipanggil setelah create/rename berhasil; kegagalan hanya di-log — kolomnya
// nullable persis untuk itu, dan baris yang terlewat bisa disapu ulang nanti
// (WHERE name_embedding IS NULL).
func (s *Outfits) embedNameAsync(outfitID, name string) {
	if s.embedder == nil {
		return
	}

	// Ambil slot tanpa menunggu. Tanpa plafon ini, satu lonjakan create saat
	// penyedia embedding lambat akan membuat satu goroutine per outfit, masing-
	// masing memegang koneksi HTTP keluar lalu mengantre di pool Postgres yang
	// cuma sepuluh — tumpukan itu yang memakan memori, bukan kerjanya sendiri.
	//
	// Penuh berarti dilewati, bukan ditunggu: menunggu di sini akan menahan
	// handler HTTP yang seharusnya sudah selesai. Melewatkannya aman karena
	// kolomnya nullable dan barisnya bisa disapu ulang nanti lewat
	// WHERE name_embedding IS NULL — itu memang alasan kolom itu dibuat
	// nullable.
	select {
	case s.embedSlots <- struct{}{}:
	default:
		slog.Warn("antrean embedding penuh; nama outfit dilewati",
			"outfitId", outfitID, "slot", cap(s.embedSlots))
		return
	}

	go func() {
		defer func() {
			<-s.embedSlots
			// Goroutine ini di luar jangkauan middleware recoverPanic, jadi
			// panic di sini akan membunuh seluruh proses. Embedding adalah
			// peningkatan opsional; tidak layak menjatuhkan server.
			if p := recover(); p != nil {
				slog.Error("panic saat meng-embed nama outfit", "outfitId", outfitID, "panic", p)
			}
		}()

		ctx, cancel := context.WithTimeout(context.Background(), embedWriteTimeout)
		defer cancel()

		embedding, err := s.embedder.Embed(ctx, name)
		if err != nil {
			slog.Warn("gagal meng-embed nama outfit", "outfitId", outfitID, "err", err)
			return
		}
		if err := s.outfits.SetNameEmbedding(ctx, outfitID, embedding); err != nil {
			// ErrNotFound berarti outfit keburu hilang; bukan masalah.
			if err != store.ErrNotFound {
				slog.Warn("gagal menyimpan embedding nama outfit", "outfitId", outfitID, "err", err)
			}
		}
	}()
}
