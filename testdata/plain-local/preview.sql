SELECT c.id, c.post_id, c.user_id, COALESCE(c.parent_id, 0), c.body, c.created_at, c.edited_at IS NOT NULL, c.deleted,
	(SELECT count(*) FROM comment_likes l WHERE l.comment_id = c.id), EXISTS (SELECT 1 FROM comment_likes l WHERE l.comment_id = c.id AND l.user_id = $1),
	(SELECT count(*) FROM comments r WHERE r.parent_id = c.id AND r.hidden_at IS NULL AND NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id = $1 AND b.target_id = r.user_id OFFSET 0)
		AND NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id = r.user_id AND b.target_id = $1 AND NOT $2 OFFSET 0)), COALESCE(c.hidden_reason, ''), c.hidden_at IS NOT NULL,
	c.ap_id IS NOT NULL, c.ap_extra FROM (SELECT * FROM (SELECT c.*, row_number() OVER (PARTITION BY c.post_id ORDER BY c.id DESC) AS rn
	FROM comments c WHERE c.post_id = ANY($3) AND c.parent_id IS NULL AND NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id = $1 AND b.target_id = c.user_id OFFSET 0)
		AND NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id = c.user_id AND b.target_id = $1 AND NOT $2 OFFSET 0)) c WHERE c.rn <= $4 ORDER BY c.id) c ORDER BY c.id
