SELECT p.id, p.user_id, p.kind, p.body, p.prop, p.skill, p.image, p.data, p.created_at,
		(SELECT count(*) FROM likes l WHERE l.post_id = p.id), EXISTS (SELECT 1 FROM likes l WHERE l.post_id = p.id AND l.user_id = $1),
		(SELECT count(*) FROM comments c LEFT JOIN comments pc ON pc.id = c.parent_id WHERE c.post_id = p.id AND NOT c.deleted AND c.hidden_at IS NULL
			AND NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id = $1 AND b.target_id = c.user_id OFFSET 0)
		AND NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id = c.user_id AND b.target_id = $1 AND NOT $6 OFFSET 0) AND (pc.id IS NULL OR NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id = $1 AND b.target_id = pc.user_id OFFSET 0)
		AND NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id = pc.user_id AND b.target_id = $1 AND NOT $6 OFFSET 0))), p.hidden_reason, p.audience, p.alt,
		p.ap_id IS NOT NULL, p.ap_extra FROM posts p WHERE p.ap_id IS NULL AND ($2 = 0 OR p.id < $2) AND ($3 = 0 OR p.user_id = $3) AND ($4 = 0 OR p.id = $4) AND ($7::bigint[] IS NULL OR p.id = ANY($7))
		AND (p.hidden_at IS NULL OR p.user_id = $1 OR $6) AND NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id = $1 AND b.target_id = p.user_id OFFSET 0)
		AND NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id = p.user_id AND b.target_id = $1 AND NOT $6 OFFSET 0) ORDER BY p.id DESC LIMIT $5
