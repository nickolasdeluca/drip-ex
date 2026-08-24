# AGENTS.md

Repo-specific instructions for coding agents. Read this before your first commit
in a session.

## What this project is

Drip is a self-hosted tunnel service (an ngrok-alike) written in Go: one binary,
`drip`, that is both the client and — via `drip server` — the server.

This fork is being turned into a **managed multi-tenant tunnel service** in the
shape of pinggy: pre-allocated tunnels bound to a client identity, an admin
panel to manage them, and a Windows service that connects and binds to its
reserved tunnel forever. The upstream single-user self-hosted mode must keep
working throughout.

## Roadmap

Six phases. Keep this list current as phases land.

1. **Control plane foundation** — SQLite store, client credentials, identity
   threaded through registration. *Done.*
2. **Wildcard TLS via certmagic** — embed ACME DNS-01 so the server obtains
   `*.<domain>` itself, with manual certs and reverse-proxy modes kept.
3. **Reservations** — pin a subdomain or TCP port to an account/client; portal
   creates the reservation, client binds it automatically at registration.
4. **Admin API + embedded UI** — on its own port, separate from tunnel traffic.
5. **Claim flow** — `active_sessions` rows, "pin this running tunnel" endpoint,
   rename-on-pin.
6. **Windows service** — `golang.org/x/sys/windows/svc` wrapper around the
   existing tunnel runner.

### Design decisions already made

- **SQLite** (`modernc.org/sqlite`, CGO-free) so the single-binary story holds.
  The store is behind a struct, not an interface; introduce an interface only if
  Postgres actually arrives.
- **Admin panel on a separate port**, never on the tunnel data path.
- **certmagic, not `x/crypto/acme/autocert`** — autocert cannot do DNS-01 and so
  cannot issue wildcards. See the trap about `internal/server/tls/autocert.go`.
- **Both reservation paths ship**: portal-first (create the reservation, then the
  client binds it) is primary; claiming a running ephemeral tunnel is secondary.
  Both write the same `tunnel_reservations` row.

## Layout

```
cmd/drip/                  entry point; both client and server
internal/client/cli/       cobra commands: http, tcp, server, admin, daemon, ...
internal/client/tcp/       client-side dialer, connection pool, yamux sessions
internal/server/tcp/       listener, per-connection handler, registration, proxy
internal/server/tunnel/    Manager: the in-memory registry of live tunnels
internal/server/proxy/     HTTP handler; subdomain routing, /stats, /metrics
internal/server/store/     SQLite control plane (accounts, clients, ...)
internal/server/auth/      credentials, Argon2id passwords, Authenticator
internal/server/tls/       autocert wrapper (currently dead code - see traps)
internal/shared/protocol/  5-byte framing, register/data-connect messages
internal/shared/           pools, qos, netutil, httputil, ui, ...
pkg/config/                server and client YAML config
deployments/               Dockerfiles, Caddyfile, example configs
```

## Commands

```bash
make build          # build bin/drip
make test           # go test -v -race -cover ./...
make e2e            # scripts/test/e2e-full.sh; starts real server + client
make fmt lint       # gofmt + golangci-lint
go test -race ./... # what CI effectively runs; keep it green
```

Always run `go vet ./...` and `go test -race ./...` before committing. The race
detector matters here: this codebase is concurrency-heavy and has had real races.

## Conventions

- **Never add yourself as a commit co-author.** No `Co-Authored-By` trailer, no
  "generated with" footer, in commit messages or PR bodies.
- Conventional commit subjects: `feat:`, `fix:`, `refactor:`, `test:`, `chore:`,
  `docs:`, optionally scoped (`fix(tcp):`). Imperative mood, lowercase.
- Errors are wrapped with `fmt.Errorf("...: %w", err)`; the message says what
  failed, not what the function is.
- Logging is `go.uber.org/zap` with typed fields, never `Sprintf` into a message.
- Comments explain *why*, not *what*. Match the existing density: exported
  identifiers get doc comments, non-obvious invariants get a sentence, ordinary
  code gets nothing.
- Table-driven tests where the cases vary; plain tests otherwise. Test names
  state the behavior (`TestAuthenticatedClientsBypassPerIPLimits`).
- No new dependencies without a reason worth stating in the commit message.

## Control plane

`internal/server/store` owns the SQLite database; `internal/server/auth` turns a
registration token into an identity.

Client credentials are `drip_<id>_<secret>`:

- `<id>` is 16 hex chars, stored in the clear as the `clients` primary key.
- `<secret>` is 24 random bytes, base64url — **it can contain `_`**, so parsing
  splits into exactly three fields (`strings.SplitN(token, "_", 3)`).
- Only `sha256(secret)` is stored. A fast hash is deliberate: the secret is 192
  bits of `crypto/rand`, so KDF cost buys nothing. Admin **passwords** are
  low-entropy human input and use Argon2id (`auth/password.go`). Do not
  "upgrade" credential hashing to Argon2id; do not downgrade passwords to SHA-256.

Server modes, selected by config:

| `db_path` | `token` | `require_auth` | Behavior |
|---|---|---|---|
| set | — | any | Per-client credentials from the database |
| — | set | any | Legacy single shared token |
| — | — | false | Anonymous: anyone may register (upstream default) |
| — | — | true | Rejected at config validation |

Migrations live in `store/schema.go` as an ordered slice; the index is the
version. **Never edit an applied migration — append a new one.**

## Traps

- **`internal/server/tls/autocert.go` is dead code and its host policy is
  broken.** `autocert.HostWhitelist(domain, "*."+domain)` does exact string
  matching, so the literal `*.example.com` never matches `foo.example.com`. Real
  TLS today is manual cert files or Caddy in front. Phase 2 replaces this file.
- **A wildcard host policy must check the subdomain against the live tunnel
  manager.** Otherwise random hostnames drive unbounded ACME issuance and burn
  the Let's Encrypt rate limit (50 certs/week per registered domain).
- **Authenticated clients bypass the per-IP tunnel cap and registration rate
  limit** (`tunnel.Manager.RegisterOwned`). Those limits exist to stop anonymous
  abuse; a fleet behind one NAT would otherwise lock itself out. Authenticated
  clients are bounded by their account limit and the global cap instead.
- **`validateMetricsAuth` returns true when the token is empty**
  (`proxy/handler.go`). Fail-open is intentional for `/stats` on a self-hosted
  box, but the admin API must never reuse it — write a fail-closed check.
- **Reservations must live outside the manager's shard maps.** `shard.used` is
  cleared on `Unregister`, so anything stored there is freed when a client
  disconnects — the opposite of what a reservation means.
- **`admin`, `api`, `www` and friends are reserved subdomains**
  (`shared/utils/subdomain.go`) and cannot be claimed by clients. The admin panel
  can safely live on one of them.
- **The credential cache has a 30s TTL.** Disabling, deleting or rotating a
  credential must call `Authenticator.Invalidate(clientID)`, or the old token
  keeps working until the TTL lapses.
- **`Authenticator.Close()` must run before the store is closed.** It drains the
  background last-seen writes; skipping it races the database shutdown.
- **Data connections re-validate the token against the group**, not against the
  server token (`tcp/data_connection_handler.go`). The group records whatever
  token registered the tunnel, so per-client credentials work — do not reintroduce
  a shared-token comparison ahead of it.
