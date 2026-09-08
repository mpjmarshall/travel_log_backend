# Runbook — bringing this stack up

For the deploy this is written against: **one traveller, who is also the
operator**, on a host they control. It is not a public-deploy guide —
`docs/BEFORE-A-PUBLIC-DEPLOY.md` is that, and it has twelve unticked boxes.

## What you must set

Copy `deploy/.env.example` to `deploy/.env` and set these. The first three ship
with guessable defaults and the stack will start happily without you touching
them.

| variable | why |
|---|---|
| `POSTGRES_PASSWORD` | defaults to `travellog`, which is in a file in this repository |
| `MINIO_ROOT_PASSWORD` | defaults to `travellogsecret`, same |
| `MINIO_ROOT_USER` | defaults to `travellog` |
| `MAIL_LOG_SENDER=1` | **the api does not boot without it.** `internal/mail` has no adapter that delivers, so sign-in codes go to the container log |
| `BIND_HOST` | `127.0.0.1` publishes to loopback only. Set it to the interface you actually want reachable |
| `S3_PUBLIC_BASE_URL` | the address a **client** resolves. Not the compose service name — a phone cannot resolve `minio` |

Set `ADMIN_PASSWORD` only if you want the panel. Empty mounts no panel at all.

**If you set it and there is no TLS in front, you also need
`ADMIN_COOKIE_INSECURE=1`** — the session cookie is `Secure` by default, so a
browser on plain `http://` will not send it back and the login silently appears
to fail. These are two switches on purpose: the log sender is the only way to
boot, and it must not be what decides whether the panel's session travels in
the clear.

## Bringing it up

```bash
cd deploy && cp .env.example .env && $EDITOR .env
make up          # builds, starts, waits for three healthy services
make migrate     # idempotent; run() also migrates before it listens
```

`make up` does not seed. `make seed` refuses a database that already has a
traveller, and prints where it was pointed.

## Getting yourself an account

Registration is invite-gated and closes to strangers without one.

```bash
make invite                                  # mints one, prints the code
curl -X POST "$BASE/v1/auth/register" \
  -d '{"email":"you@example.com","invite":"<the code>"}'
```

## Signing in, which needs the log

There is no passphrase path — it was deleted. Sign-in is code-only and the code
is delivered by whatever `mail.Sender` is wired, which here is the log.

```bash
curl -X POST "$BASE/v1/auth/code" -d '{"email":"you@example.com"}'   # always 202
docker compose -f deploy/docker-compose.yml logs api \
  | grep 'mail: not sent' | tail -1 | sed 's/^[^{]*//' | jq -r .body
curl -X POST "$BASE/v1/auth/session" \
  -d '{"email":"you@example.com","code":"<the code>"}'
```

**You will do this about once per device.** Sessions last 30 days and slide on
use, so an active client is never asked again.

## Backups

```bash
make backup      # pg_dump -Fc inside the container, keeps 7, into backups/
```

**Nothing runs this for you.** It writes to the same host as the volume it
protects, and it does **not** include the bucket — a database restore without a
bucket restore gives you a log whose every photograph reference resolves and
points at nothing. Schedule it, and copy it off the box.

## What this deploy does not have

- **TLS.** Caddy is written up as its own slice and is not in the stack.
- **Mail.** Covered above; it is why `MAIL_LOG_SENDER` exists.
- **A rate limiter that works behind a proxy.** `httpx.ClientKey` keys on
  `RemoteAddr`, which is correct for a direct connection and becomes one bucket
  for everyone the moment something proxies in front of it.
- **Bucket backup, or anything that reclaims an orphaned object** beyond
  `make sweep`, which nothing schedules either.
