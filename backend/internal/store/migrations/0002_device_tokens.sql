-- Credenciales por dispositivo para montar la plataforma como unidad de red.
--
-- No se reutiliza la contraseña del usuario: Windows la guarda en su
-- Administrador de credenciales y queda ahí indefinidamente. Un token por
-- equipo se revoca solo ese equipo, sin tocar la cuenta ni obligar a nadie a
-- cambiar su contraseña.

CREATE TABLE IF NOT EXISTS device_tokens (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- Igual que las sesiones: solo se guarda la huella del token.
    token_hash  TEXT NOT NULL UNIQUE,
    device_name TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at TIMESTAMPTZ,
    last_ip     TEXT NOT NULL DEFAULT '',
    expires_at  TIMESTAMPTZ,
    revoked_at  TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_device_tokens_user ON device_tokens(user_id);
