package service

import (
	"context"
	"fmt"

	"github.com/hanan/avatar-catalog-backend/internal/apierr"
	"github.com/hanan/avatar-catalog-backend/internal/model"
)

// MaxBatchOutfits membatasi jumlah outfit per POST /v1/outfits:batch.
//
// Angkanya dipilih dari sisi muatan, bukan dari sisi database: satu outfit
// boleh membawa MaxOutfitItems item, jadi satu batch penuh sudah berupa
// ratusan baris dalam satu transaksi. Batch yang jauh lebih besar memindahkan
// masalahnya, bukan menghilangkannya — request-nya jadi lama, dan kegagalan di
// tengah membatalkan lebih banyak pekerjaan sekaligus.
const MaxBatchOutfits = 20

// BatchOutfitResult adalah nasib satu outfit di dalam batch.
//
// Index menunjuk posisi di daftar yang klien kirim, dan itu yang membuat
// balasannya berguna: importer yang mengirim 120 outfit tahu persis baris mana
// yang gagal tanpa mencocokkan apa pun sendiri.
type BatchOutfitResult struct {
	Index  int
	Outfit model.Outfit
	// Err terisi bila outfit ini ditolak. Selalu berupa *apierr.Error, jadi
	// kode dan pesannya bisa dilaporkan apa adanya ke klien.
	Err error
}

// Created melaporkan apakah outfit ini benar-benar tersimpan.
func (r BatchOutfitResult) Created() bool { return r.Err == nil }

// CreateBatch membuat banyak outfit dalam satu permintaan.
//
// Muatan yang tidak valid TIDAK menggagalkan seluruh batch: outfit yang lolos
// pemeriksaan tetap tersimpan, dan yang ditolak dilaporkan satu per satu
// beserta index-nya. Alasannya praktis — impor puluhan outfit hasil ekspor
// klien hampir selalu punya satu dua baris cacat, dan menahan 19 baris yang
// baik karena satu baris rusak memaksa importer membelah batch sendiri.
//
// Yang tetap semua-atau-tidak adalah penulisannya: outfit yang lolos ditulis
// dalam satu transaksi, jadi kegagalan database tidak meninggalkan batch yang
// tersimpan separuh.
func (s *Outfits) CreateBatch(ctx context.Context, inputs []CreateOutfitInput) ([]BatchOutfitResult, error) {
	if len(inputs) == 0 {
		return nil, apierr.Unprocessable("empty_batch", "Field outfits wajib berisi minimal satu outfit")
	}
	if len(inputs) > MaxBatchOutfits {
		return nil, apierr.TooLarge("too_many_outfits",
			fmt.Sprintf("Maksimum %d outfit per permintaan", MaxBatchOutfits))
	}

	results := make([]BatchOutfitResult, len(inputs))
	pending := make([]model.Outfit, 0, len(inputs))
	// Menunjuk balik ke posisi di results, karena pending hanya berisi yang
	// lolos dan indeksnya tidak lagi sejajar dengan daftar kiriman klien.
	pendingAt := make([]int, 0, len(inputs))

	for i, in := range inputs {
		results[i] = BatchOutfitResult{Index: i}

		outfit, err := s.buildNewOutfit(ctx, in)
		if err != nil {
			results[i].Err = err
			continue
		}
		pending = append(pending, outfit)
		pendingAt = append(pendingAt, i)
	}

	if len(pending) == 0 {
		return results, nil
	}

	if err := s.outfits.CreateBatch(ctx, pending); err != nil {
		return nil, err
	}

	for n, outfit := range pending {
		results[pendingAt[n]].Outfit = outfit
		s.embedNameAsync(outfit.OutfitID, outfit.Name)
	}
	return results, nil
}
