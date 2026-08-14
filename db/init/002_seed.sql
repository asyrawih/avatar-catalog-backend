-- Data contoh yang sama dengan mock Postman dan dengan store.SeedData di Go.
-- Hanya untuk pengembangan.

BEGIN;

INSERT INTO player (user_id, universe_id, username, last_seen) VALUES
    (627278822, 8739264611, 'hanan_dev', '2026-08-04T11:00:00Z');

-- Satu-satunya rig yang benar-benar ada: development item milik pemilik repo.
-- Rig lain tidak diarang di sini — begitu di-upload ke Roblox dan dipakai,
-- barisnya terdaftar sendiri lewat POST /v1/outfits.
INSERT INTO body_template (template_id, name, gender, source_asset_id, created_at) VALUES
    ('88484288792766', 'Dev Rig', '?', 88484288792766, '2026-08-04T00:00:00Z');

INSERT INTO outfit
    (outfit_id, reference_id, reco_item_id, user_id, template_id, name, is_public, custom_tags, created_at, updated_at) VALUES
    ('otf_9f2a41', '550e8400-e29b-41d4-a716-446655440000', 'reco_7b31c9', 627278822, '88484288792766',
     'Y2K Streetwear',   true,  '["category:y2k","gender:male"]'::jsonb,
     '2026-07-28T09:12:44Z', '2026-08-04T11:03:19Z'),
    ('otf_3c88de', '6ba7b810-9dad-11d1-80b4-00c04fd430c8', NULL,          627278822, '88484288792766',
     'Girly Pop Casual', false, '["category:doll","gender:female"]'::jsonb,
     '2026-07-20T08:00:00Z', '2026-08-01T18:40:02Z');

-- Dua item contoh membawa adjust supaya bentuknya kelihatan saat dev; sisanya
-- sengaja NULL, karena item tanpa koreksi penempatan adalah keadaan lumrah.
INSERT INTO outfit_item (outfit_id, asset_id, slot, name, asset_type, price, adjust) VALUES
    ('otf_9f2a41', 78872304386489,  'Hair',     'BLOND BARREL TWISTS DREADS', 'HairAccessory', 69, NULL),
    ('otf_9f2a41', 14433369343,     'Jacket',   'Hero Jacket Oni Blood Moon', 'Accessory',     79, NULL),
    ('otf_9f2a41', 116123466288288, 'Face',     'Carter Shades w Goatee',     'FaceAccessory', 99,
        '{"pos": {"x": 0, "y": -0.3, "z": 0}, "rot": null, "scale": null}'),
    ('otf_3c88de', 120044550011,    'Hair',     'Pink Bow Twin Tails',        'HairAccessory', 55,
        '{"pos": {"x": 0, "y": 0.12, "z": 0}, "rot": null, "scale": {"x": 1.05, "y": 1.05, "z": 1.05}}'),
    ('otf_3c88de', 120044550012,    'Face',     'Sugar Heart Blush',          'FaceAccessory', 35, NULL),
    ('otf_3c88de', 120044550013,    'Jacket',   'Frill Cardigan',             'Accessory',     85, NULL),
    ('otf_3c88de', 120044550014,    'Pants',    'Pastel Pleated Skirt',       'Pants',         45, NULL),
    ('otf_3c88de', 120044550015,    'Shoes',    'Doll Mary Janes',            'Accessory',     60, NULL),
    ('otf_3c88de', 120044550016,    'Back',     'Chibi Star Backpack',        'Accessory',     40, NULL);

COMMIT;
