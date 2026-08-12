// Package ratelimit menyediakan pembatas jendela-tetap sederhana.
//
// Dipakai membatasi request per kunci API (lihat internal/httpapi): satu
// konsumen yang mengamuk — worker yang retry beruntun, atau klien API publik
// yang salah tulis loop — tidak boleh menghabiskan kapasitas konsumen lain.
package ratelimit

import (
	"sync"
	"time"
)

// FixedWindow membatasi jumlah kejadian per kunci dalam satu jendela waktu.
type FixedWindow struct {
	mu      sync.Mutex
	counts  map[string]window
	limit   int
	windowD time.Duration
	now     func() time.Time
	// lastPrune menahan pembersihan supaya tidak berjalan di setiap request.
	lastPrune time.Time
}

type window struct {
	startedAt time.Time
	count     int
}

// NewFixedWindow membuat pembatas dengan limit kejadian per durasi d.
func NewFixedWindow(limit int, d time.Duration) *FixedWindow {
	return &FixedWindow{
		counts:  make(map[string]window),
		limit:   limit,
		windowD: d,
		now:     func() time.Time { return time.Now().UTC() },
	}
}

// Allow mencatat satu kejadian untuk key. Jika ditolak, nilai kedua adalah
// sisa detik sebelum jendela berikutnya dibuka.
func (f *FixedWindow) Allow(key string) (bool, int) {
	f.mu.Lock()
	defer f.mu.Unlock()

	now := f.now()
	f.pruneLocked(now)

	w, ok := f.counts[key]
	if !ok || now.Sub(w.startedAt) >= f.windowD {
		f.counts[key] = window{startedAt: now, count: 1}
		return true, 0
	}

	if w.count >= f.limit {
		retry := max(int((f.windowD - now.Sub(w.startedAt)).Seconds()), 1)
		return false, retry
	}

	w.count++
	f.counts[key] = w
	return true, 0
}

// pruneInterval menahan pembersihan supaya tidak berjalan di setiap request.
const pruneInterval = time.Minute

// pruneLocked membuang jendela yang sudah lewat.
//
// Tanpa ini map tumbuh selamanya: satu entri per kunci yang pernah dipakai,
// termasuk kunci yang sudah dicabut dan alamat yang tidak pernah kembali.
// Untuk pembatasan per kunci API jumlahnya memang kecil, tapi pembatas ini
// juga dipakai dengan kunci lain — biaya membersihkannya jauh lebih murah
// daripada mencari tahu belakangan kenapa memori proses naik terus.
//
// Pemanggil wajib memegang f.mu.
func (f *FixedWindow) pruneLocked(now time.Time) {
	if now.Sub(f.lastPrune) < pruneInterval {
		return
	}
	f.lastPrune = now

	for key, w := range f.counts {
		if now.Sub(w.startedAt) >= f.windowD {
			delete(f.counts, key)
		}
	}
}
