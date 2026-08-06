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

// embedNameAsync meng-embed nama outfit di belakang layar lalu menyimpannya.
// Dipanggil setelah create/rename berhasil; kegagalan hanya di-log — kolomnya
// nullable persis untuk itu, dan baris yang terlewat bisa disapu ulang nanti
// (WHERE name_embedding IS NULL).
func (s *Outfits) embedNameAsync(outfitID, name string) {
	if s.embedder == nil {
		return
	}

	go func() {
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
