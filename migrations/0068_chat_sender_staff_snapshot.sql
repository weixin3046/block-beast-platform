-- Historical sender roles cannot be reconstructed reliably. Do not infer them
-- from current roles; only newly sent messages get an authoritative snapshot.
ALTER TABLE chat_messages ADD COLUMN sender_is_staff BOOLEAN NOT NULL DEFAULT false;
