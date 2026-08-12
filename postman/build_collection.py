"""Membangun Postman collection avatar-catalog-api dari daftar rute yang benar-benar
terdaftar di internal/httpapi/router.go."""

import json
import pathlib
import re

BASE = "{{baseUrl}}"

# Variabel Postman yang harus muncul sebagai ANGKA di JSON, bukan string.
# `"userId": "{{userId}}"` menghasilkan `"userId": "627278822"` dan ditolak
# backend (userId bertipe int64). Sentinel ini dilepas kutipnya setelah
# json.dumps.
RAW_PREFIX = "@@RAW@@"


def raw(expr):
    """Tandai nilai supaya ditulis apa adanya, tanpa tanda kutip."""
    return RAW_PREFIX + expr


def dump_body(body):
    text = json.dumps(body, indent=2, ensure_ascii=False)
    return re.sub(r'"' + RAW_PREFIX + r'([^"]*)"', r"\1", text)


def url(path, query=None):
    """Susun objek URL Postman. path tanpa garis miring di depan."""
    segments = [s for s in path.split("/") if s]
    out = {
        "raw": BASE + "/" + "/".join(segments),
        "host": [BASE],
        "path": segments,
    }
    if query:
        out["query"] = [
            {"key": k, "value": v, "description": d, "disabled": dis}
            for k, v, d, dis in query
        ]
        aktif = [f"{k}={v}" for k, v, _, dis in query if not dis]
        if aktif:
            out["raw"] += "?" + "&".join(aktif)
    return out


def req(name, method, path, *, desc, query=None, body=None, headers=None,
        tests=None, prerequest=None, auth_var=None):
    hdr = list(headers or [])
    if body is not None:
        hdr.insert(0, {"key": "Content-Type", "value": "application/json"})

    item = {
        "name": name,
        "request": {
            "method": method,
            "header": hdr,
            "url": url(path, query),
            "description": desc,
        },
        "response": [],
    }
    if body is not None:
        item["request"]["body"] = {
            "mode": "raw",
            "raw": dump_body(body),
            "options": {"raw": {"language": "json"}},
        }
    if auth_var:
        item["request"]["auth"] = {
            "type": "bearer",
            "bearer": [{"key": "token", "value": "{{" + auth_var + "}}", "type": "string"}],
        }
    events = []
    if prerequest:
        events.append({"listen": "prerequest",
                       "script": {"type": "text/javascript", "exec": prerequest}})
    if tests:
        events.append({"listen": "test",
                       "script": {"type": "text/javascript", "exec": tests}})
    if events:
        item["event"] = events
    return item


# Header yang menyatakan panggilan mewakili seorang pemain. Backend hanya
# menerimanya dari kunci ber-scope actor:assert.
AKTOR = [{"key": "X-User-Id", "value": "{{userId}}",
          "description": "Pemain yang diwakili; butuh kunci ber-scope actor:assert"}]

IDEMPOTENSI = [{"key": "Idempotency-Key", "value": "{{idempotencyKey}}",
                "description": "Dibangkitkan otomatis oleh skrip pre-request"}]

# Pre-request yang membangkitkan Idempotency-Key baru tiap kali dikirim.
GEN_KEY = [
    "// Kunci baru tiap kirim. Untuk menguji perilaku idempoten, matikan baris",
    "// ini lalu kirim dua kali dengan kunci yang sama.",
    "pm.collectionVariables.set('idempotencyKey', require('uuid').v4());",
]

OK200 = [
    "pm.test('status 200', () => pm.response.to.have.status(200));",
]


def simpan(var, path_json):
    return [
        f"if (pm.response.code < 300) {{",
        f"  const d = pm.response.json();",
        f"  pm.collectionVariables.set('{var}', {path_json});",
        f"}}",
    ]


items = []

# --- Kesehatan -------------------------------------------------------------
items.append({
    "name": "Kesehatan",
    "description": "Probe untuk orchestrator. Dua-duanya TANPA kunci API — "
                   "kubelet dan load balancer tidak memegang kredensial, dan probe "
                   "yang butuh kunci akan menandai pod sehat jadi tidak sehat "
                   "begitu kuncinya dirotasi.",
    "item": [
        req("Healthz", "GET", "healthz",
            desc="Membuktikan proses hidup. Tidak menyentuh Postgres maupun Redis.",
            tests=OK200 + ["pm.test('status ok', () => pm.response.json().status === 'ok');"],
            auth_var="noAuth"),
        req("Readyz", "GET", "readyz",
            desc="Ikut memeriksa dependensi. 503 = degraded; field `checks` "
                 "menunjukkan Postgres atau Redis yang bermasalah.",
            tests=[
                "pm.test('200 atau 503', () => pm.expect(pm.response.code).to.be.oneOf([200, 503]));",
                "const d = pm.response.json();",
                "console.log('checks:', JSON.stringify(d.checks));",
            ],
            auth_var="noAuth"),
    ],
})

# --- Outfit ----------------------------------------------------------------
outfit_body = {
    "userId": raw("{{userId}}"),
    "templateId": "{{templateId}}",
    "name": "Y2K Streetwear",
    "isPublic": True,
    "customTags": ["category:y2k", "gender:male"],
    "thumbnailAssetId": 0,
    "items": [
        {"assetId": 78872304386489, "slot": "Hair",
         "name": "BLOND BARREL TWISTS DREADS", "assetType": "HairAccessory", "price": 69},
        {"assetId": 14433369343, "slot": "Jacket",
         "name": "Hero Jacket Oni Blood Moon", "assetType": "Accessory", "price": 79},
    ],
    "body": {
        "colors": {"head": "AE7C64", "torso": "AE7C64", "leftArm": "AE7C64",
                   "rightArm": "AE7C64", "leftLeg": "AE7C64", "rightLeg": "AE7C64"},
        "scales": {"height": 1.05, "width": 1, "head": 1, "depth": 1,
                   "bodyType": 1, "proportion": 0},
    },
}

items.append({
    "name": "Outfit",
    "description": "Katalog outfit pemain. Baca butuh scope catalog:read, tulis "
                   "butuh catalog:write.",
    "item": [
        req("Daftar outfit", "GET", "v1/outfits",
            desc="Semua penyaring opsional. Tanpa userId, daftarnya mencakup semua "
                 "pemain.\n\n"
                 "`sort` menerima `recent` (bawaan), `mostLiked`, `mostViewed`. "
                 "Cursor menyimpan sort asalnya — memakai cursor mostLiked pada "
                 "sort=recent ditolak 400 cursor_sort_mismatch, bukan diam-diam "
                 "mengembalikan halaman ngawur.\n\n"
                 "Scope: catalog:read",
            query=[
                ("userId", "{{userId}}", "Kosongkan untuk daftar semua pemain", True),
                ("isPublic", "true", "true/false", True),
                ("q", "y2k", "Cocokkan sebagian nama, tanpa peduli huruf besar-kecil", True),
                ("outfitId", "{{outfitId}}", "Boleh diulang atau dipisah koma", True),
                ("sort", "mostLiked", "recent | mostLiked | mostViewed", True),
                ("limit", "20", "Bawaan 20, maksimum 100", False),
                ("cursor", "{{cursor}}", "Dari nextCursor halaman sebelumnya", True),
            ],
            headers=AKTOR,
            tests=OK200 + [
                "const d = pm.response.json();",
                "pm.test('amplop daftar', () => pm.expect(d).to.have.all.keys('data','nextCursor','hasMore'));",
                "if (d.nextCursor) pm.collectionVariables.set('cursor', d.nextCursor);",
                "if (d.data.length) pm.collectionVariables.set('outfitId', d.data[0].outfitId);",
            ]),
        req("Buat outfit", "POST", "v1/outfits",
            desc="Backend membangkitkan `referenceId` (UUID untuk "
                 "RecommendationService:RegisterItemAsync). Simpan `recoItemId` "
                 "balasannya lewat PATCH setelah RegisterItemAsync selesai.\n\n"
                 "`templateId` boleh dikirim sebagai string maupun angka.\n"
                 "`body` opsional — item saja tidak cukup untuk merender ulang avatar.\n\n"
                 "Header Idempotency-Key opsional di sini; kalau diisi, pengulangan "
                 "dengan kunci sama mengembalikan respons pertama apa adanya.\n\n"
                 "Scope: catalog:write (+ actor:assert untuk X-User-Id)",
            body=outfit_body, headers=AKTOR + IDEMPOTENSI, prerequest=GEN_KEY,
            tests=[
                "pm.test('status 201', () => pm.response.to.have.status(201));",
                "const d = pm.response.json();",
                "pm.collectionVariables.set('outfitId', d.outfitId);",
                "pm.collectionVariables.set('referenceId', d.referenceId);",
                "pm.test('referenceId dibangkitkan', () => pm.expect(d.referenceId).to.be.a('string'));",
            ]),
        req("Detail outfit", "GET", "v1/outfits/{{outfitId}}",
            desc="Outfit yang sudah di-soft-delete menjawab 410 outfit_deleted, "
                 "bukan 404 — referenceId yang sudah beredar di feed tetap bisa "
                 "dijelaskan.\n\nGET ini murni baca: tidak menaikkan viewCount.\n\n"
                 "Scope: catalog:read",
            tests=OK200),
        req("Ubah outfit", "PATCH", "v1/outfits/{{outfitId}}",
            desc="Field yang tidak dikirim dibiarkan apa adanya. Dipakai juga untuk "
                 "menyimpan `recoItemId` balasan RegisterItemAsync.\n\n"
                 "Scope: catalog:write",
            body={"name": "Y2K Streetwear v2", "isPublic": False,
                  "customTags": ["category:y2k"], "recoItemId": "reco_7b31c9"},
            headers=AKTOR, tests=OK200),
        req("Ganti seluruh item", "PUT", "v1/outfits/{{outfitId}}/items",
            desc="Mengganti isi item, bukan menambah. Satu assetId tidak boleh muncul "
                 "dua kali; satu slot boleh diisi lebih dari satu asset.\n\n"
                 "Scope: catalog:write",
            body={"items": [
                {"assetId": 78872304386489, "slot": "Hair",
                 "name": "BLOND BARREL TWISTS DREADS", "assetType": "HairAccessory", "price": 69},
            ]},
            headers=AKTOR, tests=OK200),
        req("Cari outfit", "GET", "v1/outfits/search",
            desc="Toleran salah ketik (trigram pg_trgm) dan, bila embedding terisi, "
                 "juga mirip secara makna. Hasilnya peringkat, bukan halaman — "
                 "tidak ada cursor.\n\nBeda dengan `q` di daftar outfit yang "
                 "mencocokkan sebagian nama apa adanya.\n\nScope: catalog:read",
            query=[
                ("q", "zepeto", "Kata kunci; toleran salah ketik", False),
                ("userId", "{{userId}}", "", True),
                ("isPublic", "true", "", True),
                ("limit", "20", "", True),
            ],
            tests=OK200),
        req("Resolve referenceId", "POST", "v1/outfits/resolve",
            desc="Menukar sekumpulan referenceId dari feed rekomendasi jadi metadata "
                 "render. Berhalaman: kirim daftar id yang sama persis di tiap "
                 "halaman, cursor menandai posisi di dalam daftar itu.\n\n"
                 "`notFound` hanya memuat id dari halaman ini.\n\nScope: catalog:read",
            body={"referenceIds": ["{{referenceId}}",
                                   "550e8400-e29b-41d4-a716-446655440000"]},
            query=[("limit", "20", "", True), ("cursor", "", "", True)],
            tests=OK200),
    ],
})

# --- Like & view -----------------------------------------------------------
items.append({
    "name": "Like & View",
    "description": "Popularitas outfit. Disimpan dua lapis: tabel kejadian per "
                   "pemain (bahan data latih generator outfit) plus counter di baris "
                   "outfit untuk baca cepat.",
    "item": [
        req("Sukai outfit", "POST", "v1/outfits/{{outfitId}}/likes",
            desc="Idempoten: menekan dua kali berakhir pada keadaan yang sama dan "
                 "tetap 200, dengan `changed:false` pada yang kedua — bukan 409. "
                 "Satu pemain satu like, ditegakkan primary key tabel.\n\n"
                 "Tanpa X-User-Id: 401 actor_required.\n\n"
                 "Scope: catalog:write + actor:assert",
            headers=AKTOR,
            tests=OK200 + [
                "const d = pm.response.json();",
                "pm.test('liked', () => pm.expect(d.liked).to.be.true);",
                "console.log('likeCount:', d.likeCount, 'changed:', d.changed);",
            ]),
        req("Batalkan like", "DELETE", "v1/outfits/{{outfitId}}/likes",
            desc="Idempoten juga: membatalkan like yang tidak ada menjawab 200 dengan "
                 "`changed:false`.\n\nScope: catalog:write + actor:assert",
            headers=AKTOR, tests=OK200),
        req("Catat view", "POST", "v1/outfits/{{outfitId}}/views",
            desc="Tiap panggilan menambah SATU baris kejadian — pemain yang membuka "
                 "outfit sama lima kali memang lima sinyal, dan berapa kali dilihat "
                 "sebelum disukai adalah bagian dari datanya.\n\n"
                 "Panggil saat outfit benar-benar tampil di layar pemain, bukan saat "
                 "daftarnya diambil.\n\n"
                 "X-User-Id opsional: view anonim tetap dicatat (user_id NULL), masih "
                 "berguna untuk popularitas walau tidak untuk data latih per pemain.\n\n"
                 "Scope: catalog:write",
            headers=AKTOR, tests=OK200),
        req("Daftar terpopuler (mostLiked)", "GET", "v1/outfits",
            desc="Pintasan dari daftar outfit dengan sort=mostLiked.\n\n"
                 "Paginasinya memakai keyset (likeCount, outfitId) — kunci yang "
                 "berbeda dari urutan bawaan, jadi cursornya tidak bisa dipertukarkan "
                 "antar sort.\n\n"
                 "Angkanya bisa tertinggal paling lama CACHE_TTL (bawaan 1 menit) saat "
                 "cache baca aktif. Balasan aksi like/view itu sendiri selalu akurat.\n\n"
                 "Scope: catalog:read",
            query=[("sort", "mostLiked", "", False), ("limit", "20", "", False)],
            headers=AKTOR, tests=OK200),
    ],
})

# --- Rig -------------------------------------------------------------------
items.append({
    "name": "Rig (Body Template)",
    "description": "Registry rig yang sudah di-upload ke Roblox. Roblox yang "
                   "memegang asetnya; tabel ini hanya mencatat rig mana saja yang "
                   "pernah dipakai.",
    "item": [
        req("Daftar rig", "GET", "v1/templates",
            desc="Terbaru dulu.\n\nScope: catalog:read",
            query=[("limit", "20", "", False), ("cursor", "", "", True)],
            tests=OK200),
        req("Daftarkan rig", "POST", "v1/templates",
            desc="Rig yang sudah terdaftar tidak ditimpa: nama dan gender yang sudah "
                 "diisi lewat PATCH tidak hilang hanya karena outfit baru memakai rig "
                 "yang sama. 201 kalau baru, 200 kalau sudah ada.\n\n"
                 "`templateId` adalah Roblox asset id, boleh string maupun angka.\n\n"
                 "Scope: catalog:write",
            body={"templateId": "{{templateId}}", "name": "Dev Rig", "gender": "?"},
            tests=["pm.test('200 atau 201', () => pm.expect(pm.response.code).to.be.oneOf([200, 201]));"]),
        req("Detail rig", "GET", "v1/templates/{{templateId}}",
            desc="Scope: catalog:read", tests=OK200),
        req("Ubah rig", "PATCH", "v1/templates/{{templateId}}",
            desc="Mengisi nama dan gender yang belum diketahui saat rig pertama kali "
                 "dipakai.\n\nScope: catalog:write",
            body={"name": "Dev Rig", "gender": "M"},
            tests=OK200),
    ],
})

# --- Transaksi -------------------------------------------------------------
items.append({
    "name": "Transaksi",
    "description": "Catatan pembelian dari game server. Cashback ter-accrue "
                   "otomatis saat transaksi dicatat.",
    "item": [
        req("Catat transaksi", "POST", "v1/transactions",
            desc="Header Idempotency-Key WAJIB. Idempotensinya ditegakkan kolom unik "
                 "di tabel TRANSACTION, bukan cache respons — jadi tetap berlaku walau "
                 "proses backend sempat restart. Pengulangan menjawab 200 dengan "
                 "`idempotentReplay: true`, bukan 201.\n\n"
                 "`result` per item: success | aborted | failed. Hanya `success` yang "
                 "dihitung sebagai spend (dasar cashback).\n\n"
                 "`bundleId` terisi menandai item sebagai bagian paket: harganya adalah "
                 "HARGA BUNDLE INDUK yang terulang di tiap bagian, dan perhitungan "
                 "spend menghitung tiap bundleId sekali saja.\n\n"
                 "Scope: transactions:write",
            body={
                "userId": raw("{{userId}}"),
                "universeId": 7654321,
                "placeId": 1234567,
                "jobId": "6b8f2c1e-0000-4a1b-9c3d-1f2e3d4c5b6a",
                "status": "success",
                "occurredAt": "2026-08-12T10:00:00Z",
                "items": [
                    {"assetId": 78872304386489, "price": 69, "result": "success"},
                    {"assetId": 14433369343, "price": 79, "result": "success"},
                    {"assetId": 116123466288288, "price": 45, "result": "failed"},
                ],
            },
            headers=AKTOR + IDEMPOTENSI, prerequest=GEN_KEY,
            tests=[
                "pm.test('200 atau 201', () => pm.expect(pm.response.code).to.be.oneOf([200, 201]));",
                "const d = pm.response.json();",
                "if (d.txId) pm.collectionVariables.set('txId', d.txId);",
                "console.log('idempotentReplay:', d.idempotentReplay === true);",
            ]),
        req("Riwayat transaksi", "GET", "v1/transactions",
            desc="Terbaru dulu.\n\nScope: transactions:read",
            query=[("userId", "{{userId}}", "", False), ("limit", "20", "", False),
                   ("cursor", "", "", True)],
            tests=OK200),
    ],
})

# --- Cashback --------------------------------------------------------------
items.append({
    "name": "Cashback",
    "description": "Semua nilai dalam Robux. Rate efektif = min(20% + bonus aktif, "
                   "40%); cashback per transaksi = floor(spend x rate).\n\n"
                   "PERHATIKAN PEMBAGIAN SCOPE-nya: membaca saldo (cashback:read), "
                   "membuat request redeem (cashback:redeem), dan MENUNTASKAN redeem "
                   "(cashback:admin) adalah tiga izin berbeda. Game server boleh dua "
                   "yang pertama; yang ketiga jalur uang keluar dan hanya untuk kunci "
                   "dashboard — request di folder ini yang butuh itu memakai "
                   "{{adminApiKey}}.",
    "item": [
        req("Ringkasan cashback", "GET", "v1/cashback/summary",
            desc="Saldo, rate berjalan + progres bonus, request pending.\n\n"
                 "Scope: cashback:read",
            query=[("userId", "{{userId}}", "", False)],
            headers=AKTOR, tests=OK200),
        req("Ledger cashback", "GET", "v1/cashback/entries",
            desc="Ledger append-only pemain, terbaru dulu.\n\nScope: cashback:read",
            query=[("userId", "{{userId}}", "", False), ("limit", "20", "", False),
                   ("cursor", "", "", True)],
            headers=AKTOR, tests=OK200),
        req("Buat request redeem", "POST", "v1/cashback/redeems",
            desc="Seluruh saldo dipotong dan dijadikan satu request pending; "
                 "fulfillment dikerjakan tim internal di luar sistem ini.\n\n"
                 "Minimum 100 Robux, satu pending per pemain.\n\n"
                 "Scope: cashback:redeem",
            body={"userId": raw("{{userId}}")},
            headers=AKTOR,
            tests=[
                "pm.test('200/201 atau 422 kalau saldo kurang', () => "
                "pm.expect(pm.response.code).to.be.oneOf([200, 201, 409, 422]));",
                "const d = pm.response.json();",
                "if (d.requestId) pm.collectionVariables.set('requestId', d.requestId);",
            ]),
        req("Daftar request redeem", "GET", "v1/cashback/redeems",
            desc="Tanpa `userId` = antrean internal seluruh pemain.\n\n"
                 "Scope: cashback:read",
            query=[("userId", "{{userId}}", "Kosongkan untuk antrean internal", True),
                   ("status", "pending", "pending | completed | rejected", True),
                   ("limit", "20", "", False), ("cursor", "", "", True)],
            tests=OK200),
        req("Tuntaskan request redeem", "PATCH", "v1/cashback/redeems/{{requestId}}",
            desc="`completed` setelah fulfillment selesai, `rejected` untuk "
                 "membatalkan dan mengembalikan saldo. Idempoten untuk status yang "
                 "sama.\n\n"
                 "JALUR UANG KELUAR — kunci game server sengaja TIDAK bisa "
                 "memanggilnya (403 insufficient_scope). Request ini memakai "
                 "{{adminApiKey}}.\n\n"
                 "Scope: cashback:admin",
            body={"status": "completed"}, auth_var="adminApiKey",
            tests=[
                "// Yang dibuktikan di sini scope-nya lulus, bukan bahwa ada request",
                "// pending: itu bergantung saldo pemain. 404 berarti requestId belum",
                "// terisi karena Buat request redeem belum pernah berhasil.",
                "pm.test('scope lulus (bukan 403)', () => pm.expect(pm.response.code).to.not.eql(403));",
                "if (pm.response.code === 404) {",
                "  console.log('Belum ada request redeem. Catat transaksi bernilai besar dulu, lalu Buat request redeem.');",
                "}",
            ]),
        req("Tarik cashback (reversal)", "POST", "v1/cashback/reversals",
            desc="Refund/chargeback menarik kembali cashback transaksi terkait. Saldo "
                 "boleh menjadi minus. Idempoten per txId.\n\n"
                 "JALUR UANG KELUAR — memakai {{adminApiKey}}.\n\n"
                 "Scope: cashback:admin",
            body={"txId": "{{txId}}"}, auth_var="adminApiKey",
            tests=[
                "pm.test('scope lulus (bukan 403)', () => pm.expect(pm.response.code).to.not.eql(403));",
                "if (pm.response.code >= 400) {",
                "  console.log('Butuh txId dari transaksi yang cashback-nya sudah ter-accrue:', pm.response.json().error.code);",
                "}",
            ]),
        req("Daftar event bonus", "GET", "v1/cashback/events",
            desc="Jendela bonus event yang terjadwal.\n\nScope: cashback:read",
            tests=OK200),
        req("Jadwalkan event bonus", "POST", "v1/cashback/events",
            desc="Menjadwalkan jendela bonus. Rate efektif tetap dibatasi 40%.\n\n"
                 "Memakai {{adminApiKey}}.\n\nScope: cashback:admin",
            body={"name": "Weekend Boost", "startsAt": "2026-08-15T00:00:00Z",
                  "endsAt": "2026-08-17T23:59:59Z"},
            auth_var="adminApiKey",
            tests=["pm.test('200 atau 201', () => pm.expect(pm.response.code).to.be.oneOf([200, 201]));"]),
        req("Rekonsiliasi", "GET", "v1/cashback/reconciliation",
            desc="Agregat ledger (Robux) untuk rekonsiliasi periodik.\n\n"
                 "Memakai {{adminApiKey}}.\n\nScope: cashback:admin",
            query=[("from", "2026-08-01T00:00:00Z", "RFC 3339", False),
                   ("to", "2026-08-31T23:59:59Z", "RFC 3339", False)],
            auth_var="adminApiKey", tests=OK200),
    ],
})

# --- Autentikasi (contoh penolakan) ---------------------------------------
items.append({
    "name": "Autentikasi — contoh penolakan",
    "description": "Request yang SEHARUSNYA gagal. Ada di sini supaya batas "
                   "keamanannya bisa diperiksa sendiri, bukan cuma dipercaya.",
    "item": [
        req("Tanpa kunci → 401", "GET", "v1/outfits",
            desc="Semua kegagalan autentikasi dijawab sama persis (401 "
                 "unauthenticated): bentuk token salah, keyId tidak ada, hash tidak "
                 "cocok, kunci dicabut, kunci kedaluwarsa. Membedakannya akan memberi "
                 "tahu penyerang seberapa jauh tebakannya sudah benar.",
            auth_var="noAuth",
            tests=[
                "pm.test('status 401', () => pm.response.to.have.status(401));",
                "pm.test('kode unauthenticated', () => "
                "pm.expect(pm.response.json().error.code).to.eql('unauthenticated'));",
            ]),
        req("Kunci AI mencoba menulis → 403", "POST", "v1/outfits",
            desc="Kunci role `ai` hanya punya catalog:read + transactions:read. "
                 "Isi {{aiApiKey}} dengan kunci role ai untuk mencobanya.",
            body=outfit_body, auth_var="aiApiKey",
            tests=[
                "pm.test('status 403', () => pm.response.to.have.status(403));",
                "pm.test('kode insufficient_scope', () => "
                "pm.expect(pm.response.json().error.code).to.eql('insufficient_scope'));",
            ]),
        req("Kunci dashboard menyamar jadi pemain → 403", "GET", "v1/outfits",
            desc="Menyatakan diri bertindak atas nama pemain (header X-User-Id) adalah "
                 "kemampuan tersendiri: scope actor:assert. Hanya kunci game server "
                 "yang punya. Kunci dashboard atau AI yang bocor tetap tidak bisa "
                 "menyukai atau menukar cashback atas nama siapa pun.",
            headers=AKTOR, auth_var="adminApiKey",
            tests=[
                "pm.test('status 403', () => pm.response.to.have.status(403));",
                "pm.test('kode actor_assert_forbidden', () => "
                "pm.expect(pm.response.json().error.code).to.eql('actor_assert_forbidden'));",
            ]),
        req("Game server menuntaskan redeem → 403", "PATCH",
            "v1/cashback/redeems/{{requestId}}",
            desc="Pembagian scope yang paling penting: kunci game server yang bocor "
                 "tidak bisa mencairkan apa pun.",
            body={"status": "completed"},
            tests=[
                "pm.test('status 403', () => pm.response.to.have.status(403));",
                "pm.test('kode insufficient_scope', () => "
                "pm.expect(pm.response.json().error.code).to.eql('insufficient_scope'));",
            ]),
        req("Sort tidak dikenal → 400", "GET", "v1/outfits",
            desc="Parameter yang salah ditolak terang-terangan, bukan diam-diam "
                 "jatuh ke urutan bawaan.",
            query=[("sort", "terpopuler", "", False)],
            tests=[
                "pm.test('status 400', () => pm.response.to.have.status(400));",
                "pm.test('kode invalid_sort', () => "
                "pm.expect(pm.response.json().error.code).to.eql('invalid_sort'));",
            ]),
    ],
})

items.append({
    "name": "Pembersihan",
    "description": "Sengaja folder terakhir. Soft delete membuat outfit menjawab "
                   "410 pada semua endpoint lain, jadi menjalankannya lebih awal akan "
                   "menggagalkan folder Like & View saat collection dijalankan "
                   "berurutan dengan Collection Runner.",
    "item": [
        req("Hapus outfit (soft delete)", "DELETE", "v1/outfits/{{outfitId}}",
            desc="Hanya mengisi `deletedAt`; barisnya tidak pernah dihapus — "
                 "referenceId yang sudah beredar di feed tetap bisa dijelaskan.\n\n"
                 "Responsnya membawa `recoItemId` sebagai pengingat memanggil "
                 "RemoveItemAsync; kalau tidak, outfit yang sudah dihapus tetap "
                 "muncul di feed rekomendasi.\n\n"
                 "Setelah ini, GET outfit yang sama menjawab 410 outfit_deleted, "
                 "bukan 404.\n\nScope: catalog:write",
            headers=AKTOR, tests=OK200),
    ],
})

collection = {
    "info": {
        "_postman_id": "a7c3f1e2-0b4d-4c8a-9e6f-2d5b8c1a4e70",
        "name": "avatar-catalog-api",
        "description": (
            "Backend katalog avatar Roblox.\n\n"
            "## Sebelum mulai\n\n"
            "1. Import `avatar-catalog-api.postman_environment.json` sebagai "
            "environment, atau isi variabel collection di bawah.\n"
            "2. Terbitkan kunci API di backend:\n\n"
            "```bash\n"
            "export DATABASE_URL=postgres://...\n"
            "go run ./cmd/apikey issue --name postman --role game-server --env test\n"
            "go run ./cmd/apikey issue --name postman-admin --role dashboard --env test\n"
            "go run ./cmd/apikey issue --name postman-ai --role ai --env test\n"
            "```\n\n"
            "3. Isi `apiKey` (game-server), `adminApiKey` (dashboard), dan `aiApiKey` "
            "(ai).\n\n"
            "Untuk pengembangan lokal tanpa kunci sama sekali, jalankan server dengan "
            "`AUTH_REQUIRED=false` — tapi jangan pernah di lingkungan yang bisa "
            "dijangkau dari luar.\n\n"
            "## Konvensi\n\n"
            "- POST yang membuat baris memakai header `Idempotency-Key` "
            "(dibangkitkan otomatis oleh skrip pre-request).\n"
            "- PUT/PATCH/DELETE idempoten dari desainnya.\n"
            "- Paginasi memakai cursor, bukan offset. Cursor buram; kirim balik apa "
            "adanya dari `nextCursor`.\n"
            "- Header `X-User-Id` menyatakan panggilan mewakili seorang pemain. "
            "Backend hanya menerimanya dari kunci ber-scope `actor:assert`.\n"
            "- Semua kegagalan memakai satu amplop: "
            "`{\"error\":{\"code\":\"...\",\"message\":\"...\",\"details\":[...]}}`.\n\n"
            "## Variabel yang terisi otomatis\n\n"
            "`outfitId`, `referenceId`, `txId`, `requestId`, dan `cursor` diisi oleh "
            "skrip test dari respons sebelumnya, jadi folder bisa dijalankan berurutan "
            "dengan Collection Runner.\n\n"
            "Dibuat dari rute yang benar-benar terdaftar di "
            "`internal/httpapi/router.go`."
        ),
        "schema": "https://schema.getpostman.com/json/collection/v2.1.0/collection.json",
    },
    "auth": {
        "type": "bearer",
        "bearer": [{"key": "token", "value": "{{apiKey}}", "type": "string"}],
    },
    "event": [
        {
            "listen": "prerequest",
            "script": {
                "type": "text/javascript",
                "exec": [
                    "// Peringatkan sekali kalau kunci belum diisi — gejalanya (401 di",
                    "// semua request) mudah disalahartikan sebagai server yang rusak.",
                    "if (!pm.collectionVariables.get('apiKey') && !pm.environment.get('apiKey')) {",
                    "  console.warn('apiKey belum diisi. Terbitkan dengan: go run ./cmd/apikey issue --name postman --role game-server --env test');",
                    "}",
                ],
            },
        }
    ],
    "variable": [
        {"key": "baseUrl", "value": "http://localhost:8080",
         "description": "Tanpa garis miring di akhir"},
        {"key": "apiKey", "value": "",
         "description": "Kunci role game-server. acb_test_... atau acb_live_..."},
        {"key": "adminApiKey", "value": "",
         "description": "Kunci role dashboard; dipakai endpoint cashback:admin"},
        {"key": "aiApiKey", "value": "",
         "description": "Kunci role ai; dipakai contoh penolakan scope"},
        {"key": "noAuth", "value": "",
         "description": "Sengaja kosong: dipakai request yang harus jalan TANPA kunci"},
        {"key": "userId", "value": "627278822",
         "description": "Pemain contoh dari data seed"},
        {"key": "templateId", "value": "88484288792766",
         "description": "Rig contoh dari data seed"},
        {"key": "outfitId", "value": "otf_9f2a41",
         "description": "Diisi otomatis oleh Buat outfit / Daftar outfit"},
        {"key": "referenceId", "value": "550e8400-e29b-41d4-a716-446655440000",
         "description": "Diisi otomatis oleh Buat outfit"},
        {"key": "txId", "value": "tx_belum_ada",
         "description": "Diisi otomatis oleh Catat transaksi. Nilai bawaannya sekadar "
                        "penampung supaya rute yang memakainya tetap cocok"},
        {"key": "requestId", "value": "req_belum_ada",
         "description": "Diisi otomatis oleh Buat request redeem. Nilai bawaannya sekadar "
                        "penampung supaya rute yang memakainya tetap cocok"},
        {"key": "cursor", "value": "", "description": "Diisi otomatis oleh Daftar outfit"},
        {"key": "idempotencyKey", "value": "",
         "description": "Dibangkitkan skrip pre-request tiap kirim"},
    ],
    "item": items,
}

out = pathlib.Path("/Users/dxh4nan/Projects/avatar-catalog-backend/postman")
(out / "avatar-catalog-api.postman_collection.json").write_text(
    json.dumps(collection, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")

env = {
    "id": "b2d4f6a8-1c3e-4570-8a9b-0d1e2f3a4b5c",
    "name": "avatar-catalog — lokal",
    "values": [
        {"key": "baseUrl", "value": "http://localhost:8080", "type": "default", "enabled": True},
        {"key": "apiKey", "value": "", "type": "secret", "enabled": True},
        {"key": "adminApiKey", "value": "", "type": "secret", "enabled": True},
        {"key": "aiApiKey", "value": "", "type": "secret", "enabled": True},
        {"key": "userId", "value": "627278822", "type": "default", "enabled": True},
        {"key": "templateId", "value": "88484288792766", "type": "default", "enabled": True},
    ],
    "_postman_variable_scope": "environment",
}
(out / "avatar-catalog-api.postman_environment.json").write_text(
    json.dumps(env, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")

total = sum(len(f["item"]) for f in items)
print(f"folder: {len(items)}, request: {total}")
