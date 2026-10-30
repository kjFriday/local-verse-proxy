# local-verse-proxy

One lock, many keys. A LiteLLM proxy on your LAN that holds every
upstream secret and hands out scoped, revocable keys to each seat.

## Why one lock, many keys

Without a proxy, every machine and person that talks to a model holds
the real provider key. Leak one laptop and you rotate everywhere.

With the proxy:

- **The lock** is `LITELLM_MASTER_KEY`. It lives in `seats.env` on the
  proxy box and is used only to mint and revoke keys.
- **Upstream keys** (Anthropic, etc.) also live only in `seats.env`.
  Seats never see them.
- **Each seat** gets its own virtual key, limited to certain models,
  a dollar budget and an expiry. Revoke one seat; nobody else notices.

```
 seat A --key A--\
 seat B --key B---> proxy :4000 --upstream keys--> Ollama / Anthropic
 seat C --key C--/      ^
                        | master key (mint/revoke only)
                    mint-key.sh
```

## Files

| File               | What it is                                  |
|--------------------|---------------------------------------------|
| `config.yaml`      | Models and where their secrets come from    |
| `seats.env.example`| Template for the secrets file (`seats.env`) |
| `mint-key.sh`      | Mint or revoke a seat's key                 |
| `compose.yaml`     | Runs the proxy and its Postgres             |

LiteLLM stores virtual keys in Postgres, so the compose file starts one
next to the proxy.

## Setup

Needs Docker, `curl` and `jq`.

```sh
cp seats.env.example seats.env
$EDITOR seats.env          # master key, salt, DB password, upstreams
docker compose up -d
```

## Mint a key

```sh
./mint-key.sh laptop                       # defaults from seats.env
./mint-key.sh desk --models local,claude --budget 20 --duration 90d
```

It prints only the new key. Hand that to the seat.

## Test it (definition of done)

```sh
KEY=$(./mint-key.sh test-seat --models local --budget 1)

# 1. Scoped key works
curl -s localhost:4000/v1/chat/completions \
  -H "Authorization: Bearer $KEY" -H 'Content-Type: application/json' \
  -d '{"model":"local","messages":[{"role":"user","content":"hi"}]}'

# 2. Model outside its scope is refused
curl -s localhost:4000/v1/chat/completions \
  -H "Authorization: Bearer $KEY" -H 'Content-Type: application/json' \
  -d '{"model":"claude","messages":[{"role":"user","content":"hi"}]}'

# 3. Revoked key is refused
./mint-key.sh --revoke "$KEY"
curl -s -o /dev/null -w '%{http_code}\n' localhost:4000/v1/models \
  -H "Authorization: Bearer $KEY"          # expect 401
```

## Rules

- Never commit `seats.env`. It is in `.gitignore`.
- Never give a seat the master key.
- Never change `LITELLM_SALT_KEY` after first start.
