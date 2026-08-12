package idempotency

// Tes di file ini ada di dalam paket, bukan di idempotency_test, karena perlu
// mengendalikan jam: penyapuan diredam satu menit, dan menunggu semenit
// sungguhan di setiap kali test dijalankan tidak masuk akal.

import (
	"fmt"
	"testing"
	"time"
)

// Ini tes terpenting untuk paket ini.
//
// Kunci idempotensi unik per request: ditulis sekali lalu tidak pernah dibaca
// lagi, karena retry justru kejadian langka. Kalau kedaluwarsa hanya diperiksa
// saat Get, entri yang tidak pernah dibaca ulang tidak akan pernah dibuang —
// dan setiap POST menambah satu rekaman berisi body respons, selamanya, sampai
// proses kehabisan memori.
func TestRekamanLamaDisapuWalauTidakPernahDibacaUlang(t *testing.T) {
	now := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)
	store := NewMemoryStore(time.Hour)
	store.now = func() time.Time { return now }

	// Seribu kunci sekali pakai, persis seperti trafik sungguhan: tidak satu
	// pun dibaca ulang.
	for i := 0; i < 1000; i++ {
		store.Put("outfits.create", fmt.Sprintf("kunci-%d", i),
			Record{Status: 201, Body: []byte(`{"outfitId":"otf_x"}`)})
	}
	if store.Len() != 1000 {
		t.Fatalf("tersimpan %d rekaman, ingin 1000", store.Len())
	}

	// Majukan jam melewati TTL, lalu tulis satu kunci baru. Penulisan itulah
	// yang memicu penyapuan.
	now = now.Add(2 * time.Hour)
	store.Put("outfits.create", "kunci-baru", Record{Status: 201})

	if got := store.Len(); got != 1 {
		t.Errorf("tersisa %d rekaman setelah penyapuan, ingin 1 (hanya yang baru)", got)
	}
}

// Penyapuan menelusuri seluruh map, jadi menjalankannya di setiap penulisan
// mengubah biaya penyimpanan jadi kuadratik terhadap jumlah rekaman.
func TestPenyapuanDiredam(t *testing.T) {
	now := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)
	store := NewMemoryStore(time.Nanosecond) // semuanya langsung kedaluwarsa
	store.now = func() time.Time { return now }

	for i := 0; i < 100; i++ {
		store.Put("s", fmt.Sprintf("k-%d", i), Record{Status: 201})
	}

	// Jam tidak bergerak, jadi jeda satu menit belum terlampaui: walau semua
	// rekaman sudah kedaluwarsa, tidak ada yang disapu.
	if got := store.Len(); got != 100 {
		t.Errorf("tersisa %d rekaman, ingin 100; penyapuan tampaknya berjalan di setiap penulisan", got)
	}
}

// Penyapuan tidak boleh membuang rekaman yang masih berlaku.
func TestPenyapuanTidakMembuangYangMasihBerlaku(t *testing.T) {
	now := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)
	store := NewMemoryStore(time.Hour)
	store.now = func() time.Time { return now }

	store.Put("s", "lama", Record{Status: 201})
	now = now.Add(30 * time.Minute) // belum lewat TTL
	store.Put("s", "baru", Record{Status: 201})

	now = now.Add(31 * time.Minute) // "lama" lewat TTL, "baru" belum
	store.Put("s", "pemicu", Record{Status: 201})

	if _, ok := store.Get("s", "lama"); ok {
		t.Error("rekaman yang sudah lewat TTL masih ada")
	}
	if _, ok := store.Get("s", "baru"); !ok {
		t.Error("rekaman yang masih berlaku ikut terbuang")
	}
}
