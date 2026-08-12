package ratelimit_test

import (
	"sync"
	"testing"
	"time"

	"github.com/hanan/avatar-catalog-backend/internal/ratelimit"
)

func TestMenolakSetelahLimitTercapai(t *testing.T) {
	limiter := ratelimit.NewFixedWindow(3, time.Second)

	for i := 1; i <= 3; i++ {
		if ok, _ := limiter.Allow("a"); !ok {
			t.Fatalf("request ke-%d ditolak, ingin diterima", i)
		}
	}

	ok, retryAfter := limiter.Allow("a")
	if ok {
		t.Error("request ke-4 diterima, ingin ditolak")
	}
	if retryAfter < 1 {
		t.Errorf("retryAfter = %d, ingin minimal 1 detik", retryAfter)
	}
}

// Tiap kunci punya jatahnya sendiri: satu konsumen yang mengamuk tidak boleh
// menghabiskan kapasitas konsumen lain.
func TestJatahTerpisahPerKunci(t *testing.T) {
	limiter := ratelimit.NewFixedWindow(2, time.Second)

	for i := 0; i < 2; i++ {
		limiter.Allow("a")
	}
	if ok, _ := limiter.Allow("a"); ok {
		t.Fatal("kunci a belum tertolak padahal jatahnya habis")
	}

	if ok, _ := limiter.Allow("b"); !ok {
		t.Error("kunci b ikut tertolak padahal jatahnya sendiri masih ada")
	}
}

func TestJatahPulihSetelahJendelaLewat(t *testing.T) {
	// Jendela sangat pendek supaya test tidak perlu menunggu lama.
	limiter := ratelimit.NewFixedWindow(1, 20*time.Millisecond)

	if ok, _ := limiter.Allow("a"); !ok {
		t.Fatal("request pertama ditolak")
	}
	if ok, _ := limiter.Allow("a"); ok {
		t.Fatal("request kedua diterima padahal jatahnya satu")
	}

	time.Sleep(30 * time.Millisecond)

	if ok, _ := limiter.Allow("a"); !ok {
		t.Error("jatah tidak pulih setelah jendela lewat")
	}
}

// Pembatas dipanggil dari banyak goroutine sekaligus; hitungannya tidak boleh
// meleset. Jalankan dengan -race untuk memeriksanya.
func TestAmanDipakaiBersamaan(t *testing.T) {
	const limit = 100
	limiter := ratelimit.NewFixedWindow(limit, time.Minute)

	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		diterima int
	)
	for i := 0; i < limit*2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ok, _ := limiter.Allow("a"); ok {
				mu.Lock()
				diterima++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if diterima != limit {
		t.Errorf("diterima %d dari %d request, ingin tepat %d", diterima, limit*2, limit)
	}
}
