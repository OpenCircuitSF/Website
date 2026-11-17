-- #0704: engagement snapshots of guide posts, the guide_posts counterpart of
-- outreach_engagement. The outreach tracker writes one row per capture.
CREATE TABLE guide_post_engagement (
    id          BIGSERIAL PRIMARY KEY,
    post_id     BIGINT NOT NULL REFERENCES guide_posts (id) ON DELETE CASCADE,
    captured_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    likes       INT NOT NULL DEFAULT 0,
    reposts     INT NOT NULL DEFAULT 0,
    replies     INT NOT NULL DEFAULT 0,
    quotes      INT NOT NULL DEFAULT 0,
    CONSTRAINT guide_post_engagement_counts_check
        CHECK (likes >= 0 AND reposts >= 0 AND replies >= 0 AND quotes >= 0)
);

CREATE INDEX guide_post_engagement_post_captured_idx ON guide_post_engagement (post_id, captured_at DESC);
