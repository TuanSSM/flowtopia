package main

// The statements as they were before FMD 1 (origin/main at d780ef5, printed from that tree), against which
// unlimited is held: an instance that limits no domain, and local mode above all, runs them byte for byte.

import "testing"

func TestUnlimitedIsBefore(t *testing.T) {
	local := &app{}
	for name, c := range map[string]struct{ got, want string }{
		"bell": {func() string { l, _ := local.notifStatements(); return l }(), `SELECT n.id, n.kind, COALESCE(n.actor_id, 0), COALESCE(n.post_id, 0), COALESCE(n.comment_id, 0),
		COALESCE(c.parent_id, c.id, 0), n.text, n.read, n.created_at
		FROM notifications n LEFT JOIN comments c ON c.id = n.comment_id
		WHERE n.user_id = $1 AND (n.kind IN ('achievement', 'moderation') OR n.actor_id IS NULL OR NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id = $1 AND b.target_id = n.actor_id OFFSET 0)
		AND NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id = n.actor_id AND b.target_id = $1 AND NOT false OFFSET 0)) ORDER BY n.id DESC LIMIT $2`},
		"comment page": {unlimited(commentsSQL(commentPageSQL)), `SELECT c.id, c.post_id, c.user_id, COALESCE(c.parent_id, 0), c.body, c.created_at, c.edited_at IS NOT NULL, c.deleted,
	(SELECT count(*) FROM comment_likes l WHERE l.comment_id = c.id), EXISTS (SELECT 1 FROM comment_likes l WHERE l.comment_id = c.id AND l.user_id = $1),
	(SELECT count(*) FROM comments r WHERE r.parent_id = c.id AND r.hidden_at IS NULL AND NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id = $1 AND b.target_id = r.user_id OFFSET 0)
		AND NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id = r.user_id AND b.target_id = $1 AND NOT $2 OFFSET 0)
		AND (r.ap_id IS NULL OR r.deleted OR NOT EXISTS (SELECT 1 FROM ap_actors sa WHERE sa.user_id = r.user_id AND sa.state <> 'ok' OFFSET 0))), COALESCE(c.hidden_reason, ''), c.hidden_at IS NOT NULL,
	c.ap_id IS NOT NULL, c.ap_extra FROM (SELECT * FROM comments c WHERE c.post_id = $3 AND c.parent_id IS NULL AND c.id > $4 AND NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id = $1 AND b.target_id = c.user_id OFFSET 0)
		AND NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id = c.user_id AND b.target_id = $1 AND NOT $2 OFFSET 0) AND (c.deleted OR NOT EXISTS (SELECT 1 FROM ap_actors sa WHERE sa.user_id = c.user_id AND sa.state <> 'ok' OFFSET 0)) ORDER BY c.id LIMIT $5) c ORDER BY c.id`},
		"comment shown": {local.limitStatements(commentShown), `comments c JOIN posts p ON p.id = c.post_id LEFT JOIN comments rc ON rc.id = c.parent_id WHERE c.id = $1
		AND (c.hidden_at IS NULL OR c.user_id = $2 OR $3) AND (p.hidden_at IS NULL OR p.user_id = $2 OR $3)
		AND NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id = $2 AND b.target_id = c.user_id OFFSET 0)
		AND NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id = c.user_id AND b.target_id = $2 AND NOT $3 OFFSET 0) AND NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id = $2 AND b.target_id = p.user_id OFFSET 0)
		AND NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id = p.user_id AND b.target_id = $2 AND NOT $3 OFFSET 0) AND (rc.id IS NULL OR NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id = $2 AND b.target_id = rc.user_id OFFSET 0)
		AND NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id = rc.user_id AND b.target_id = $2 AND NOT $3 OFFSET 0))
		AND (p.ap_id IS NULL OR $3 OR (EXISTS (SELECT 1 FROM follows rf WHERE rf.follower_id = $2 AND rf.followee_id = p.user_id AND rf.state = 'accepted')
		OR $2 = ANY(p.ap_to) AND EXISTS (SELECT 1 FROM users mu WHERE mu.id = $2 AND (mu.federates OR mu.owner)
			AND (mu.suspended_until IS NULL OR mu.suspended_until <= now())))
		AND NOT EXISTS (SELECT 1 FROM ap_actors sa WHERE sa.user_id = p.user_id AND sa.state <> 'ok' OFFSET 0))`},
		"feed": {unlimited(feedSQL), `SELECT p.id, p.user_id, p.kind, p.body, p.prop, p.skill, p.image, p.data, p.created_at,
		(SELECT count(*) FROM likes l WHERE l.post_id = p.id), EXISTS (SELECT 1 FROM likes l WHERE l.post_id = p.id AND l.user_id = $1),
		(SELECT count(*) FROM comments c LEFT JOIN comments pc ON pc.id = c.parent_id WHERE c.post_id = p.id AND NOT c.deleted AND c.hidden_at IS NULL
			AND NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id = $1 AND b.target_id = c.user_id OFFSET 0)
		AND NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id = c.user_id AND b.target_id = $1 AND NOT $6 OFFSET 0) AND (pc.id IS NULL OR NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id = $1 AND b.target_id = pc.user_id OFFSET 0)
		AND NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id = pc.user_id AND b.target_id = $1 AND NOT $6 OFFSET 0))
			AND (c.ap_id IS NULL OR NOT EXISTS (SELECT 1 FROM ap_actors sa WHERE sa.user_id = c.user_id AND sa.state <> 'ok' OFFSET 0)) AND (pc.ap_id IS NULL OR pc.deleted OR NOT EXISTS (SELECT 1 FROM ap_actors sa WHERE sa.user_id = pc.user_id AND sa.state <> 'ok' OFFSET 0))), p.hidden_reason, p.audience, p.alt,
		p.ap_id IS NOT NULL, p.ap_extra FROM posts p WHERE p.ap_id IS NULL AND ($2 = 0 OR p.id < $2) AND ($3 = 0 OR p.user_id = $3) AND ($4 = 0 OR p.id = $4) AND ($7::bigint[] IS NULL OR p.id = ANY($7))
		AND (p.hidden_at IS NULL OR p.user_id = $1 OR $6) AND NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id = $1 AND b.target_id = p.user_id OFFSET 0)
		AND NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id = p.user_id AND b.target_id = $1 AND NOT $6 OFFSET 0) ORDER BY p.id DESC LIMIT $5`},
		"home": {unlimited(homeSQL), `SELECT p.id, p.user_id, p.kind, p.body, p.prop, p.skill, p.image, p.data, p.created_at,
		(SELECT count(*) FROM likes l WHERE l.post_id = p.id), EXISTS (SELECT 1 FROM likes l WHERE l.post_id = p.id AND l.user_id = $1),
		(SELECT count(*) FROM comments c LEFT JOIN comments pc ON pc.id = c.parent_id WHERE c.post_id = p.id AND NOT c.deleted AND c.hidden_at IS NULL
			AND NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id = $1 AND b.target_id = c.user_id OFFSET 0)
		AND NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id = c.user_id AND b.target_id = $1 AND NOT $4 OFFSET 0) AND (pc.id IS NULL OR NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id = $1 AND b.target_id = pc.user_id OFFSET 0)
		AND NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id = pc.user_id AND b.target_id = $1 AND NOT $4 OFFSET 0))
			AND (c.ap_id IS NULL OR NOT EXISTS (SELECT 1 FROM ap_actors sa WHERE sa.user_id = c.user_id AND sa.state <> 'ok' OFFSET 0)) AND (pc.ap_id IS NULL OR pc.deleted OR NOT EXISTS (SELECT 1 FROM ap_actors sa WHERE sa.user_id = pc.user_id AND sa.state <> 'ok' OFFSET 0))), p.hidden_reason, p.audience, p.alt,
		p.ap_id IS NOT NULL, p.ap_extra FROM (SELECT h.id FROM
		(SELECT x.id FROM (SELECT $1::bigint AS id UNION ALL SELECT f.followee_id FROM follows f WHERE f.follower_id = $1 AND f.state = 'accepted') x
			WHERE NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id = $1 AND b.target_id = x.id OFFSET 0)
		AND NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id = x.id AND b.target_id = $1 AND NOT $4 OFFSET 0) AND NOT EXISTS (SELECT 1 FROM ap_actors sa WHERE sa.user_id = x.id AND sa.state <> 'ok' OFFSET 0)) a
		CROSS JOIN LATERAL (SELECT p.id FROM posts p WHERE p.user_id = a.id AND ($2 = 0 OR p.id < $2)
			AND (p.hidden_at IS NULL OR p.user_id = $1 OR $4) AND ($5 OR p.ap_id IS NULL) ORDER BY p.id DESC LIMIT $3) h
		ORDER BY h.id DESC LIMIT $3) h JOIN posts p ON p.id = h.id ORDER BY p.id DESC`},
		"local home": {local.localStatements(homeSQL), `SELECT p.id, p.user_id, p.kind, p.body, p.prop, p.skill, p.image, p.data, p.created_at,
		(SELECT count(*) FROM likes l WHERE l.post_id = p.id), EXISTS (SELECT 1 FROM likes l WHERE l.post_id = p.id AND l.user_id = $1),
		(SELECT count(*) FROM comments c LEFT JOIN comments pc ON pc.id = c.parent_id WHERE c.post_id = p.id AND NOT c.deleted AND c.hidden_at IS NULL
			AND NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id = $1 AND b.target_id = c.user_id OFFSET 0)
		AND NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id = c.user_id AND b.target_id = $1 AND NOT $4 OFFSET 0) AND (pc.id IS NULL OR NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id = $1 AND b.target_id = pc.user_id OFFSET 0)
		AND NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id = pc.user_id AND b.target_id = $1 AND NOT $4 OFFSET 0))), p.hidden_reason, p.audience, p.alt,
		p.ap_id IS NOT NULL, p.ap_extra FROM (SELECT h.id FROM
		(SELECT x.id FROM (SELECT $1::bigint AS id UNION ALL SELECT f.followee_id FROM follows f WHERE f.follower_id = $1 AND f.state = 'accepted') x
			WHERE NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id = $1 AND b.target_id = x.id OFFSET 0)
		AND NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id = x.id AND b.target_id = $1 AND NOT $4 OFFSET 0) AND NOT EXISTS (SELECT 1 FROM ap_actors sa WHERE sa.user_id = x.id AND sa.state <> 'ok' OFFSET 0)) a
		CROSS JOIN LATERAL (SELECT p.id FROM posts p WHERE p.user_id = a.id AND ($2 = 0 OR p.id < $2)
			AND (p.hidden_at IS NULL OR p.user_id = $1 OR $4) AND ($5 OR p.ap_id IS NULL) ORDER BY p.id DESC LIMIT $3) h
		ORDER BY h.id DESC LIMIT $3) h JOIN posts p ON p.id = h.id ORDER BY p.id DESC`},
		"local post": {local.localStatements(postOneSQL), `SELECT p.id, p.user_id, p.kind, p.body, p.prop, p.skill, p.image, p.data, p.created_at,
		(SELECT count(*) FROM likes l WHERE l.post_id = p.id), EXISTS (SELECT 1 FROM likes l WHERE l.post_id = p.id AND l.user_id = $1),
		(SELECT count(*) FROM comments c LEFT JOIN comments pc ON pc.id = c.parent_id WHERE c.post_id = p.id AND NOT c.deleted AND c.hidden_at IS NULL
			AND NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id = $1 AND b.target_id = c.user_id OFFSET 0)
		AND NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id = c.user_id AND b.target_id = $1 AND NOT $3 OFFSET 0) AND (pc.id IS NULL OR NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id = $1 AND b.target_id = pc.user_id OFFSET 0)
		AND NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id = pc.user_id AND b.target_id = $1 AND NOT $3 OFFSET 0))), p.hidden_reason, p.audience, p.alt,
		p.ap_id IS NOT NULL, p.ap_extra FROM posts p WHERE p.id = $2 AND (p.hidden_at IS NULL OR p.user_id = $1 OR $3)
		AND NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id = $1 AND b.target_id = p.user_id OFFSET 0)
		AND NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id = p.user_id AND b.target_id = $1 AND NOT $3 OFFSET 0) AND (p.ap_id IS NULL OR $4 AND ($3 OR (EXISTS (SELECT 1 FROM follows rf WHERE rf.follower_id = $1 AND rf.followee_id = p.user_id AND rf.state = 'accepted')
		OR $1 = ANY(p.ap_to) AND EXISTS (SELECT 1 FROM users mu WHERE mu.id = $1 AND (mu.federates OR mu.owner)
			AND (mu.suspended_until IS NULL OR mu.suspended_until <= now())))
		AND NOT EXISTS (SELECT 1 FROM ap_actors sa WHERE sa.user_id = p.user_id AND sa.state <> 'ok' OFFSET 0)))`},
		"post": {unlimited(postOneSQL), `SELECT p.id, p.user_id, p.kind, p.body, p.prop, p.skill, p.image, p.data, p.created_at,
		(SELECT count(*) FROM likes l WHERE l.post_id = p.id), EXISTS (SELECT 1 FROM likes l WHERE l.post_id = p.id AND l.user_id = $1),
		(SELECT count(*) FROM comments c LEFT JOIN comments pc ON pc.id = c.parent_id WHERE c.post_id = p.id AND NOT c.deleted AND c.hidden_at IS NULL
			AND NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id = $1 AND b.target_id = c.user_id OFFSET 0)
		AND NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id = c.user_id AND b.target_id = $1 AND NOT $3 OFFSET 0) AND (pc.id IS NULL OR NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id = $1 AND b.target_id = pc.user_id OFFSET 0)
		AND NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id = pc.user_id AND b.target_id = $1 AND NOT $3 OFFSET 0))
			AND (c.ap_id IS NULL OR NOT EXISTS (SELECT 1 FROM ap_actors sa WHERE sa.user_id = c.user_id AND sa.state <> 'ok' OFFSET 0)) AND (pc.ap_id IS NULL OR pc.deleted OR NOT EXISTS (SELECT 1 FROM ap_actors sa WHERE sa.user_id = pc.user_id AND sa.state <> 'ok' OFFSET 0))), p.hidden_reason, p.audience, p.alt,
		p.ap_id IS NOT NULL, p.ap_extra FROM posts p WHERE p.id = $2 AND (p.hidden_at IS NULL OR p.user_id = $1 OR $3)
		AND NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id = $1 AND b.target_id = p.user_id OFFSET 0)
		AND NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id = p.user_id AND b.target_id = $1 AND NOT $3 OFFSET 0) AND (p.ap_id IS NULL OR $4 AND ($3 OR (EXISTS (SELECT 1 FROM follows rf WHERE rf.follower_id = $1 AND rf.followee_id = p.user_id AND rf.state = 'accepted')
		OR $1 = ANY(p.ap_to) AND EXISTS (SELECT 1 FROM users mu WHERE mu.id = $1 AND (mu.federates OR mu.owner)
			AND (mu.suspended_until IS NULL OR mu.suspended_until <= now())))
		AND NOT EXISTS (SELECT 1 FROM ap_actors sa WHERE sa.user_id = p.user_id AND sa.state <> 'ok' OFFSET 0)))`},
		"preview": {unlimited(commentsSQL(feedCommentsSQL)), `SELECT c.id, c.post_id, c.user_id, COALESCE(c.parent_id, 0), c.body, c.created_at, c.edited_at IS NOT NULL, c.deleted,
	(SELECT count(*) FROM comment_likes l WHERE l.comment_id = c.id), EXISTS (SELECT 1 FROM comment_likes l WHERE l.comment_id = c.id AND l.user_id = $1),
	(SELECT count(*) FROM comments r WHERE r.parent_id = c.id AND r.hidden_at IS NULL AND NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id = $1 AND b.target_id = r.user_id OFFSET 0)
		AND NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id = r.user_id AND b.target_id = $1 AND NOT $2 OFFSET 0)
		AND (r.ap_id IS NULL OR r.deleted OR NOT EXISTS (SELECT 1 FROM ap_actors sa WHERE sa.user_id = r.user_id AND sa.state <> 'ok' OFFSET 0))), COALESCE(c.hidden_reason, ''), c.hidden_at IS NOT NULL,
	c.ap_id IS NOT NULL, c.ap_extra FROM (SELECT * FROM (SELECT c.*, row_number() OVER (PARTITION BY c.post_id ORDER BY c.ap_id IS NULL DESC, c.id DESC) AS rn
	FROM (SELECT x.* FROM unnest($3::bigint[]) AS q(pid) CROSS JOIN LATERAL (
		(SELECT c.* FROM comments c WHERE c.post_id = q.pid AND c.parent_id IS NULL AND c.ap_id IS NULL AND NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id = $1 AND b.target_id = c.user_id OFFSET 0)
		AND NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id = c.user_id AND b.target_id = $1 AND NOT $2 OFFSET 0) AND (c.deleted OR NOT EXISTS (SELECT 1 FROM ap_actors sa WHERE sa.user_id = c.user_id AND sa.state <> 'ok' OFFSET 0)) ORDER BY c.id DESC LIMIT $4)
		UNION ALL
		(SELECT c.* FROM comments c WHERE c.post_id = q.pid AND c.parent_id IS NULL AND c.ap_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id = $1 AND b.target_id = c.user_id OFFSET 0)
		AND NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id = c.user_id AND b.target_id = $1 AND NOT $2 OFFSET 0) AND (c.deleted OR NOT EXISTS (SELECT 1 FROM ap_actors sa WHERE sa.user_id = c.user_id AND sa.state <> 'ok' OFFSET 0)) ORDER BY c.id DESC LIMIT $4)) x) c) c
	WHERE c.rn <= $4 ORDER BY c.id) c ORDER BY c.id`},
		"replies": {unlimited(commentsSQL(repliesSQL)), `SELECT c.id, c.post_id, c.user_id, COALESCE(c.parent_id, 0), c.body, c.created_at, c.edited_at IS NOT NULL, c.deleted,
	(SELECT count(*) FROM comment_likes l WHERE l.comment_id = c.id), EXISTS (SELECT 1 FROM comment_likes l WHERE l.comment_id = c.id AND l.user_id = $1),
	(SELECT count(*) FROM comments r WHERE r.parent_id = c.id AND r.hidden_at IS NULL AND NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id = $1 AND b.target_id = r.user_id OFFSET 0)
		AND NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id = r.user_id AND b.target_id = $1 AND NOT $2 OFFSET 0)
		AND (r.ap_id IS NULL OR r.deleted OR NOT EXISTS (SELECT 1 FROM ap_actors sa WHERE sa.user_id = r.user_id AND sa.state <> 'ok' OFFSET 0))), COALESCE(c.hidden_reason, ''), c.hidden_at IS NOT NULL,
	c.ap_id IS NOT NULL, c.ap_extra FROM (SELECT * FROM (SELECT c.*, row_number() OVER (PARTITION BY c.parent_id ORDER BY c.id DESC) AS rn
		FROM comments c WHERE c.parent_id = ANY($3) AND NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id = $1 AND b.target_id = c.user_id OFFSET 0)
		AND NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id = c.user_id AND b.target_id = $1 AND NOT $2 OFFSET 0) AND (c.deleted OR NOT EXISTS (SELECT 1 FROM ap_actors sa WHERE sa.user_id = c.user_id AND sa.state <> 'ok' OFFSET 0))) c WHERE c.rn <= $4) c ORDER BY c.id`},
		"reply page": {unlimited(commentsSQL(replyPageSQL)), `SELECT c.id, c.post_id, c.user_id, COALESCE(c.parent_id, 0), c.body, c.created_at, c.edited_at IS NOT NULL, c.deleted,
	(SELECT count(*) FROM comment_likes l WHERE l.comment_id = c.id), EXISTS (SELECT 1 FROM comment_likes l WHERE l.comment_id = c.id AND l.user_id = $1),
	(SELECT count(*) FROM comments r WHERE r.parent_id = c.id AND r.hidden_at IS NULL AND NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id = $1 AND b.target_id = r.user_id OFFSET 0)
		AND NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id = r.user_id AND b.target_id = $1 AND NOT $2 OFFSET 0)
		AND (r.ap_id IS NULL OR r.deleted OR NOT EXISTS (SELECT 1 FROM ap_actors sa WHERE sa.user_id = r.user_id AND sa.state <> 'ok' OFFSET 0))), COALESCE(c.hidden_reason, ''), c.hidden_at IS NOT NULL,
	c.ap_id IS NOT NULL, c.ap_extra FROM (SELECT * FROM comments c WHERE c.parent_id = $3 AND c.id > $4 AND NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id = $1 AND b.target_id = c.user_id OFFSET 0)
		AND NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id = c.user_id AND b.target_id = $1 AND NOT $2 OFFSET 0) AND (c.deleted OR NOT EXISTS (SELECT 1 FROM ap_actors sa WHERE sa.user_id = c.user_id AND sa.state <> 'ok' OFFSET 0)) ORDER BY c.id LIMIT $5) c ORDER BY c.id`},
		"talk": {unlimited(postTalkShown), `(p.ap_id IS NULL OR p.audience IN ('public', 'unlisted') AND (EXISTS (SELECT 1 FROM follows rf WHERE rf.follower_id = $2 AND rf.followee_id = p.user_id AND rf.state = 'accepted')
		OR $2 = ANY(p.ap_to) AND EXISTS (SELECT 1 FROM users mu WHERE mu.id = $2 AND (mu.federates OR mu.owner)
			AND (mu.suspended_until IS NULL OR mu.suspended_until <= now())))
		AND NOT EXISTS (SELECT 1 FROM ap_actors sa WHERE sa.user_id = p.user_id AND sa.state <> 'ok' OFFSET 0)) AND (p.hidden_at IS NULL OR p.user_id = $2 OR $3) AND NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id = $2 AND b.target_id = p.user_id OFFSET 0)
		AND NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id = p.user_id AND b.target_id = $2 AND NOT $3 OFFSET 0)`},
		"unread": {func() string { _, u := local.notifStatements(); return u }(), `SELECT count(*) FROM notifications n WHERE n.user_id = $1 AND NOT n.read AND (n.kind IN ('achievement', 'moderation') OR n.actor_id IS NULL OR NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id = $1 AND b.target_id = n.actor_id OFFSET 0)
		AND NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id = n.actor_id AND b.target_id = $1 AND NOT false OFFSET 0))`},
	} {
		if c.got != c.want {
			t.Errorf("%s: not the statement it was before FMD 1:\n%s\nwant\n%s", name, c.got, c.want)
		}
	}
}
