ALTER TABLE chat_messages ADD COLUMN image_upload_id UUID REFERENCES uploads(id);
CREATE INDEX chat_messages_image_visible ON chat_messages(image_upload_id,room_id) WHERE image_upload_id IS NOT NULL AND status='visible';
