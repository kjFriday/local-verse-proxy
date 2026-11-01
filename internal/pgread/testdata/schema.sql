-- The part of LiteLLM v1.106.6's database that Front Desk and the
-- game read (column names and types as LiteLLM makes them), with a
-- few rows. The ci.yml pg job loads it; go test -tags pg reads it.
CREATE TABLE "LiteLLM_VerificationToken" (
  token text PRIMARY KEY,
  key_name text,
  key_alias text,
  spend double precision NOT NULL DEFAULT 0.0,
  expires timestamp,
  models text[],
  max_budget double precision,
  budget_duration text,
  budget_reset_at timestamp,
  blocked boolean,
  metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at timestamp DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE "LiteLLM_DeletedVerificationToken" (
  id text PRIMARY KEY,
  token text NOT NULL,
  key_alias text,
  spend double precision NOT NULL DEFAULT 0.0,
  deleted_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE "LiteLLM_DailyUserSpend" (
  id text PRIMARY KEY,
  user_id text,
  date text NOT NULL,
  api_key text NOT NULL,
  model text,
  model_group text,
  custom_llm_provider text,
  prompt_tokens bigint NOT NULL DEFAULT 0,
  completion_tokens bigint NOT NULL DEFAULT 0,
  spend double precision NOT NULL DEFAULT 0.0,
  api_requests bigint NOT NULL DEFAULT 0,
  failed_requests bigint NOT NULL DEFAULT 0
);
CREATE TABLE "LiteLLM_SpendLogs" (
  request_id text PRIMARY KEY,
  call_type text NOT NULL DEFAULT '',
  api_key text NOT NULL DEFAULT '',
  spend double precision NOT NULL DEFAULT 0.0,
  total_tokens integer NOT NULL DEFAULT 0,
  "startTime" timestamp NOT NULL,
  "endTime" timestamp NOT NULL,
  model text NOT NULL DEFAULT '',
  metadata jsonb DEFAULT '{}'::jsonb,
  request_tags jsonb DEFAULT '[]'::jsonb,
  status text
);

INSERT INTO "LiteLLM_VerificationToken"
  (token, key_name, key_alias, spend, models, max_budget,
   budget_duration, budget_reset_at)
VALUES
  ('tok-laptop', 'sk-...aaaa', 'laptop', 0.25, '{local}', 5, NULL, NULL),
  ('tok-fern', 'sk-...bbbb', 'game-fern', 0.004, '{local,pro}', 0.015,
   '2m', now() AT TIME ZONE 'utc' + interval '1 minute');
INSERT INTO "LiteLLM_DeletedVerificationToken" (id, token, key_alias)
VALUES ('del-1', 'tok-gone', 'old-seat');
INSERT INTO "LiteLLM_DailyUserSpend"
  (id, date, api_key, model, model_group, prompt_tokens,
   completion_tokens, spend, api_requests, failed_requests)
VALUES
  ('d1', to_char(now() AT TIME ZONE 'UTC', 'YYYY-MM-DD'), 'tok-laptop',
   'ollama/local', 'local', 100, 50, 0.25, 3, 1),
  ('d2', '2026-01-02', 'tok-gone', 'ollama/local', 'local', 10, 5, 1.5,
   2, 0),
  ('d3', '2026-01-02', 'tok-fern', 'x', 'pro', 7, 3, 0.004, 1, 0);
INSERT INTO "LiteLLM_SpendLogs"
  (request_id, spend, total_tokens, "startTime", "endTime", metadata,
   request_tags, status)
VALUES
  ('r1', 0.002, 40, now() AT TIME ZONE 'utc', now() AT TIME ZONE 'utc',
   '{"user_api_key_alias": "game-fern"}',
   '["lvg:abc123", "day:1.ab12", "seat:game-fern", "quest:d1q01"]',
   'success');
-- A table a write would change, to show none does.
CREATE TABLE pgread_canary (n int);
INSERT INTO pgread_canary VALUES (1);
