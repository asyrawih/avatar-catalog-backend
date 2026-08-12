--!strict
-- AvatarCatalog — klien HTTP untuk avatar-catalog-backend.
--
-- TARUH DI ServerScriptService (atau ServerStorage). Modul ini TIDAK BOLEH
-- dipakai dari LocalScript: klien Roblox berjalan di mesin pemain, dan apa pun
-- yang sampai ke sana bisa dibaca exploiter — termasuk kunci API.
--
-- Pemasangan
-- ----------
-- 1. Terbitkan kunci di backend:
--      DATABASE_URL=... go run ./cmd/apikey issue \
--        --name roblox-game-server-prod --role game-server --expires 90d
--
-- 2. Simpan tokennya sebagai Secret di Creator Hub
--    (Creator Dashboard → Experience → Secrets):
--      nama   : AVATAR_CATALOG_KEY
--      domain : api.contoh.com          -- domain backend, tanpa https://
--
--    Simpan sebagai Secret, bukan string di dalam Script: nilai Secret tidak
--    pernah jadi string biasa di Luau, jadi tidak bisa bocor lewat source,
--    print, maupun pesan error.
--
-- 3. Aktifkan HTTP request di Game Settings → Security.
--
-- Pemakaian
-- ---------
--   local AvatarCatalog = require(script.Parent.AvatarCatalog)
--   local api = AvatarCatalog.new("https://api.contoh.com")
--
--   local ok, outfits = api:listOutfits({ sort = "mostLiked", limit = 20 })
--   local ok2 = api:likeOutfit(player.UserId, "otf_9f2a41")

local HttpService = game:GetService("HttpService")
local RunService = game:GetService("RunService")

local SECRET_NAME = "AVATAR_CATALOG_KEY"

-- Backend membalas 429 dengan Retry-After saat dibatasi, dan 5xx saat sedang
-- bermasalah. Keduanya layak diulang; 4xx lainnya tidak — permintaannya memang
-- salah, mengulang hanya menghabiskan kuota.
local MAX_ATTEMPTS = 3
local BASE_BACKOFF = 0.5

-- encodeQuery merangkai query string. Dideklarasikan sebelum dipakai: local
-- function yang ditulis di bawah tidak terlihat oleh fungsi di atasnya.
local function encodeQuery(query: { [string]: any }?): string
	if query == nil or next(query) == nil then
		return ""
	end

	local parts = {}
	for key, value in query do
		if value ~= nil then
			table.insert(parts, HttpService:UrlEncode(tostring(key)) .. "=" .. HttpService:UrlEncode(tostring(value)))
		end
	end
	if #parts == 0 then
		return ""
	end
	return "?" .. table.concat(parts, "&")
end

export type Result = {
	ok: boolean,
	status: number,
	body: any,
	-- errorCode terisi dari amplop error backend, mis. "insufficient_scope".
	errorCode: string?,
	errorMessage: string?,
}

local AvatarCatalog = {}
AvatarCatalog.__index = AvatarCatalog

export type AvatarCatalog = typeof(setmetatable(
	{} :: { baseUrl: string, secret: Secret? },
	AvatarCatalog
))

-- new membuat klien untuk satu backend. baseUrl tanpa garis miring di akhir.
function AvatarCatalog.new(baseUrl: string): AvatarCatalog
	assert(RunService:IsServer(), "AvatarCatalog hanya boleh dipakai dari server; kunci API tidak boleh sampai ke klien")
	assert(typeof(baseUrl) == "string" and baseUrl ~= "", "baseUrl wajib diisi")

	local self = setmetatable({}, AvatarCatalog)
	self.baseUrl = string.gsub(baseUrl, "/+$", "")

	-- GetSecret melempar kalau Secret belum dipasang. Ditangkap di sini supaya
	-- pesannya menjelaskan apa yang harus dilakukan, bukan sekadar melempar
	-- dari dalam request pertama yang kebetulan jalan duluan.
	local ok, secret = pcall(function()
		return HttpService:GetSecret(SECRET_NAME)
	end)
	if not ok then
		error(string.format(
			"Secret %q belum dipasang. Buat di Creator Dashboard → Experience → Secrets, "
				.. "isi dengan token dari `apikey issue`, dan set domainnya ke host backend.",
			SECRET_NAME
		))
	end
	self.secret = secret
	return self
end

-- request menjalankan satu panggilan HTTP.
--
-- userId opsional: diisi saat panggilan mewakili seorang pemain (like, view,
-- membuat outfit, redeem). Backend hanya menerimanya dari kunci ber-scope
-- actor:assert — kunci dashboard tidak bisa menyamar jadi pemain.
function AvatarCatalog:request(
	method: string,
	path: string,
	body: any?,
	userId: number?
): Result
	local headers: { [string]: any } = {
		-- Secret:AddPrefix mengembalikan Secret baru, tetap tanpa pernah
		-- menjadi string biasa. Inilah alasan tokennya tidak bisa bocor lewat
		-- log: tidak ada titik di mana ia berbentuk teks.
		["Authorization"] = self.secret:AddPrefix("Bearer "),
	}
	if body ~= nil then
		headers["Content-Type"] = "application/json"
	end
	if userId ~= nil then
		headers["X-User-Id"] = tostring(userId)
	end

	local options = {
		Url = self.baseUrl .. path,
		Method = method,
		Headers = headers,
		Body = if body ~= nil then HttpService:JSONEncode(body) else nil,
	}

	local lastResult: Result = { ok = false, status = 0, body = nil }
	for attempt = 1, MAX_ATTEMPTS do
		local ok, response = pcall(function()
			return HttpService:RequestAsync(options)
		end)

		if not ok then
			-- Gagal di level jaringan; response berisi pesan error.
			lastResult = { ok = false, status = 0, body = nil, errorMessage = tostring(response) }
		else
			local parsed: any = nil
			if response.Body ~= nil and response.Body ~= "" then
				local decoded, decodeResult = pcall(function()
					return HttpService:JSONDecode(response.Body)
				end)
				parsed = if decoded then decodeResult else response.Body
			end

			lastResult = {
				ok = response.Success,
				status = response.StatusCode,
				body = parsed,
			}
			if not response.Success and typeof(parsed) == "table" and typeof(parsed.error) == "table" then
				lastResult.errorCode = parsed.error.code
				lastResult.errorMessage = parsed.error.message
			end

			if response.Success then
				return lastResult
			end
			-- 4xx selain 429 adalah permintaan yang memang salah; mengulang
			-- tidak akan mengubah hasilnya.
			if response.StatusCode < 500 and response.StatusCode ~= 429 then
				return lastResult
			end
		end

		if attempt < MAX_ATTEMPTS then
			task.wait(BASE_BACKOFF * 2 ^ (attempt - 1))
		end
	end
	return lastResult
end

-- --- outfit ---------------------------------------------------------------

-- listOutfits membaca daftar outfit.
-- query: { userId, isPublic, q, sort, limit, cursor }
function AvatarCatalog:listOutfits(query: { [string]: any }?): Result
	return self:request("GET", "/v1/outfits" .. encodeQuery(query))
end

function AvatarCatalog:getOutfit(outfitId: string): Result
	return self:request("GET", "/v1/outfits/" .. HttpService:UrlEncode(outfitId))
end

-- createOutfit menyimpan outfit milik seorang pemain.
function AvatarCatalog:createOutfit(userId: number, outfit: { [string]: any }): Result
	outfit.userId = userId
	return self:request("POST", "/v1/outfits", outfit, userId)
end

-- --- like dan view --------------------------------------------------------

-- likeOutfit idempoten: menekan dua kali tetap 200, dengan changed=false pada
-- yang kedua. Tidak perlu menyimpan sendiri apakah pemain sudah menyukainya.
function AvatarCatalog:likeOutfit(userId: number, outfitId: string): Result
	return self:request("POST", "/v1/outfits/" .. HttpService:UrlEncode(outfitId) .. "/likes", nil, userId)
end

function AvatarCatalog:unlikeOutfit(userId: number, outfitId: string): Result
	return self:request("DELETE", "/v1/outfits/" .. HttpService:UrlEncode(outfitId) .. "/likes", nil, userId)
end

-- recordView mencatat satu kali outfit dilihat. Tiap panggilan menambah satu
-- baris — panggil saat outfit benar-benar tampil di layar pemain, bukan saat
-- daftarnya diambil.
function AvatarCatalog:recordView(userId: number?, outfitId: string): Result
	return self:request("POST", "/v1/outfits/" .. HttpService:UrlEncode(outfitId) .. "/views", nil, userId)
end

-- --- transaksi dan cashback ----------------------------------------------

-- recordTransaction mewajibkan idempotencyKey. Kalau game server mengulang
-- karena jaringan putus, backend mengenali kunci yang sama dan tidak mencatat
-- pembelian dua kali.
function AvatarCatalog:recordTransaction(userId: number, tx: { [string]: any }): Result
	assert(typeof(tx.idempotencyKey) == "string" and tx.idempotencyKey ~= "",
		"idempotencyKey wajib diisi supaya percobaan ulang tidak tercatat dua kali")
	tx.userId = userId
	return self:request("POST", "/v1/transactions", tx, userId)
end

function AvatarCatalog:cashbackSummary(userId: number): Result
	return self:request("GET", "/v1/cashback/summary?userId=" .. tostring(userId), nil, userId)
end

function AvatarCatalog:createRedeem(userId: number, payload: { [string]: any }?): Result
	local body = payload or {}
	body.userId = userId
	return self:request("POST", "/v1/cashback/redeems", body, userId)
end

return AvatarCatalog
