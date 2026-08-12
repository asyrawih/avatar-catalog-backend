package httpapi

// Tes di dalam paket karena captureWriter tidak diekspor.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCaptureMerekamResponsKecil(t *testing.T) {
	rec := &captureWriter{ResponseWriter: httptest.NewRecorder(), status: http.StatusOK}

	body := `{"outfitId":"otf_9f2a41"}`
	if _, err := rec.Write([]byte(body)); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	if !rec.replayable() {
		t.Error("respons kecil tidak layak diputar ulang")
	}
	if got := rec.body.String(); got != body {
		t.Errorf("body terekam = %q, ingin %q", got, body)
	}
}

// Tanpa batas ini, satu respons besar akan disimpan utuh di penyimpan
// idempotensi dan bertahan selama TTL. maxBodyBytes hanya membatasi body
// MASUK, bukan keluar.
func TestCaptureBerhentiDiAtasBatas(t *testing.T) {
	inner := httptest.NewRecorder()
	rec := &captureWriter{ResponseWriter: inner, status: http.StatusOK}

	besar := strings.Repeat("a", maxCapturedBody+1)
	if _, err := rec.Write([]byte(besar)); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	if rec.replayable() {
		t.Error("respons yang melewati batas ditandai layak diputar ulang")
	}
	if rec.body.Len() != 0 {
		t.Errorf("buffer masih menahan %d byte; seharusnya dilepaskan", rec.body.Len())
	}

	// Klien tetap menerima respons utuh — pembatasan ini soal apa yang
	// DISIMPAN, bukan apa yang dikirim.
	if inner.Body.Len() != len(besar) {
		t.Errorf("klien menerima %d byte, ingin %d", inner.Body.Len(), len(besar))
	}
}

// Batasnya berlaku pada total, bukan per panggilan Write: handler menulis
// dalam banyak potongan kecil dan totalnya bisa melewati batas.
func TestCaptureMenjumlahkanPotongan(t *testing.T) {
	rec := &captureWriter{ResponseWriter: httptest.NewRecorder(), status: http.StatusOK}

	potongan := strings.Repeat("b", 1024)
	for i := 0; i < maxCapturedBody/1024+1; i++ {
		if _, err := rec.Write([]byte(potongan)); err != nil {
			t.Fatalf("Write() error = %v", err)
		}
	}

	if rec.replayable() {
		t.Error("penulisan bertahap melewati batas tapi masih ditandai layak simpan")
	}
}

// Respons gagal tidak pernah disimpan: yang diputar ulang harus hasil yang
// benar-benar terjadi, bukan kegagalan sesaat yang mungkin sudah pulih.
func TestCaptureTidakMenyimpanResponsGagal(t *testing.T) {
	rec := &captureWriter{ResponseWriter: httptest.NewRecorder(), status: http.StatusOK}
	rec.WriteHeader(http.StatusInternalServerError)
	if _, err := rec.Write([]byte(`{"error":{}}`)); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	if rec.replayable() {
		t.Error("respons 500 ditandai layak diputar ulang")
	}
}
