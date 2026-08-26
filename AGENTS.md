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
   `*.<domain>` itself, with manual certs and reverse-proxy modes kept. *Done.*
3. **Reservations** — pin a subdomain or TCP port to an account/client; portal
   creates the reservation, client binds it automatically at registration. *Done.*
4. **Admin API + embedded UI** — on its own port, separate from tunnel traffic,
   optionally published on the server domain as well. *Done.*
5. **Claim flow** — `active_sessions` rows, "pin this running tunnel" endpoint,
   rename-on-pin. *Done.*
6. **Windows service** — `golang.org/x/sys/windows/svc` wrapper around the
   tunnel runner. *Done.*

### Design decisions already made

- **SQLite** (`modernc.org/sqlite`, CGO-free) so the single-binary story holds.
  The store is behind a struct, not an interface; introduce an interface only if
  Postgres actually arrives.
- **Admin panel on a separate port**, never on the tunnel data path. It may
  *additionally* be published on the server domain (`admin_public`), which is a
  host-matched mount on the public listener, not a move: the panel keeps its own
  listener, because first-run setup is served there and nowhere else.
- **certmagic, not `x/crypto/acme/autocert`** — autocert cannot do DNS-01 and so
  cannot issue wildcards. The old `autocert.go` was dead code whose host policy
  was broken anyway (`HostWhitelist` does exact matching, so the literal
  `*.example.com` never matched a real subdomain); it is gone.
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
internal/server/reservations/  which subdomain or port a client may bind
internal/server/tls/       TLS modes: none, manual certs, ACME DNS-01 wildcard
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
- **Admin panel copy lives in `admin/ui/i18n.js`, never as a literal in
  `app.js` or `index.html`.** Views call `t('key')`; static markup carries
  `data-i18n`. English and `pt-BR` ship, the language is picked from
  `localStorage` then the browser, and both dictionaries must define the same
  keys. Server messages pass through untranslated unless `serverError` has an
  `err.<exact message>` entry for them.

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

## Reservations

`internal/server/reservations` decides what a registering client may bind.
`RegistrationHandler.Register` calls it before allocating anything, then hands
the resolved name to `tunnel.Manager`.

Resolution order:

1. **Client asked for a name.** If a reservation owns it: same account, enabled,
   and either unbound or bound to this client, it binds. Otherwise the
   registration is refused - never silently downgraded to a random name.
   If nobody owns it, it becomes an ephemeral tunnel.
2. **Client asked for nothing.** The first enabled reservation bound to this
   client that no live tunnel already holds is bound automatically. This is the
   Windows-service path: install the credential, connect, always land on the
   same URL.
3. **Neither.** A random subdomain, exactly as upstream Drip behaves.

`reservations_only: true` removes step 3 and turns the deployment into a closed
fleet where every tunnel is pre-allocated. It requires `db_path`.

Two rules that are easy to get wrong:

- A client whose reservations are *all* live gets `ErrReservationInUse`, not a
  random subdomain. Handing out a random name there would look like the
  reservation had been lost.
- Unauthenticated and legacy-token registrations have no account, so they can
  never take a reserved name by asking for it. `checkOwnership` treats an empty
  `AccountID` as a mismatch; do not "simplify" that check.

`http` and `https` share one reservation family (`NormalizeTunnelType`): a
reserved subdomain is a name, and the same name serves both. `tcp` reservations
pin a port instead, and the manager keys them by the derived `tcp-<port>`.

Reservations survive client deletion: the `clients` foreign key is
`ON DELETE SET NULL`, so the name stays with the account and only the binding
drops. Deleting the *account* cascades the reservations away with it.

## Live sessions and the claim flow

`active_sessions` is what is connected *right now*, as the control plane sees
it. `RegistrationHandler.recordSession` writes a row after the manager accepts a
registration, and `ConnectionLifecycleManager.Close` deletes it. The row carries
what the manager never keeps: the reservation that was bound, the client's local
port, the remote IP and the start time.

- **The manager stays the authority on what is live.** Sessions are bookkeeping
  laid alongside it: `GET /api/tunnels` reads the manager and decorates each
  entry from the session table, and `GET /api/sessions` drops rows the manager
  does not know about rather than reporting them as connected.
- **Recording never costs a client its tunnel.** A failed insert is logged and
  swallowed; the tunnel serves traffic, it just is not pinnable until it
  reconnects.
- **Rows are purged at startup.** They describe one process, so anything a
  previous run left behind is stale, and its name has to be free again.
- **Teardown deletes the row before unregistering from the manager.** The
  subdomain index is unique, and a client that reconnects the instant the name
  frees up would otherwise collide with its own stale row.

`POST /api/sessions/{id}/pin` is the second reservation path: instead of
reserving a name and waiting for a client to bind it, an operator pins a tunnel
that is already up. It writes the same `tunnel_reservations` row the portal-first
path writes, bound to the session's account and client.

- **The pin lands on the next reconnect, never on the live tunnel.** The running
  tunnel resolved its name before the reservation existed. Passing a different
  `subdomain` is rename-on-pin: the reservation takes the new name, the live
  tunnel keeps the old one, and the response carries `renamed: true` so the
  panel can say so.
- **A pin that kept the name links the session to the reservation**
  (`SetSessionReservation`); a rename leaves it null, because the live tunnel is
  not the thing that was reserved.
- **Unauthenticated tunnels cannot be pinned.** Anonymous and legacy shared-token
  registrations have no account or client, and a reservation with an invented
  owner is one no client can ever bind. The endpoint refuses them with an error
  that says to issue a credential first.
- **TCP sessions pin their port**, not a name; asking to rename one is a 400.

### The control stream and rebind

Pinning also asks the live client to move immediately, so an operator never has
to reach the machine to restart a service. The message rides a **control
stream**, and `POST .../pin` reports `rebound: true` when it was sent.

- **The client opens the control stream, never the server.** The server opens a
  stream per proxied request, so a stream it opened could not be told apart from
  traffic — and on a TCP tunnel the first bytes come from an untrusted visitor,
  who could otherwise forge control messages. A stream the client opened on its
  authenticated session cannot be spoofed. The server accepts it in
  `Connection.acceptControlStreams`; anything the server accepts is by
  definition a control stream, because data never flows that way.
- **Capabilities are negotiated both ways.** The client sets
  `RegisterRequest.SupportsControl`, the server answers with
  `RegisterResponse.SupportsControl`, and the client opens the stream only when
  the server advertised it. An older client opens nothing and an older server
  accepts nothing, so both fall back to "the allocation waits for the next
  reconnect".
- **`FrameTypeRebind` (0x09) is server to client.** The client writes nothing;
  the server's watcher treats the first read error as "the stream is gone".
- **A rebind is acted on by reconnecting.** The name is decided at registration,
  so the client records the target, drops the session, and asks for it on the
  way back up. `PendingRebind` outranks the sticky name both runners keep
  (`tunnel_supervisor.go`, `tunnel_runner.go`) — without that override the
  client would reconnect asking for the name it already had and never move.
- **An empty subdomain in a rebind is an instruction, not an absence.** It means
  "ask for nothing", which lands the client on whatever reservation resolves for
  it. `PendingRebind` returns a separate bool for exactly this reason.
- **Sending a rebind is best effort and never fails the pin.** The reservation
  is correct either way; `ErrNoControlStream` is expected often enough that it
  is not even logged.

## The patch-in builder

`POST /api/provision` (`admin/api_provision.go`) renders the command an operator
pastes on a machine, in the shape of the panel's "Patch in" tab. Deployment
stays two steps: the operator installs the binary however that fleet installs
binaries, and this writes only the configuration half — `drip config set`,
`drip config tunnel add`, and optionally the step that leaves it running. It
renders no installer and must not grow into one.

- **The command is rendered in Go, not in the panel.** Shell and PowerShell
  quoting is what makes it safe, and it is worth a table test; the panel only
  copies the string it was handed.
- **An existing credential renders `PASTE_TOKEN_HERE`.** Only `sha256(secret)`
  is stored, so the token of a credential issued earlier is unrecoverable — the
  builder either issues a fresh one (`new_client`, which returns the token
  exactly once, as `POST /api/clients` does) or leaves the placeholder. The
  placeholder is deliberately free of shell metacharacters: `<TOKEN>` would be a
  redirection in sh.
- **Allocations are adopted, not fought over.** A name or port the account
  already holds is reused and bound to the machine being patched in, so
  rebuilding a command for a deployed machine is safe; one owned by another
  account or already bound to another machine is refused.
- **A TCP tunnel gets no `--subdomain`.** The reservation pins the port and the
  client binds it by asking for nothing, which is resolution step 2.
- **Windows autostart passes `--reseed`.** The step before it rewrote the user
  config, and `service install` keeps an existing `%ProgramData%` copy, so
  without it the service would read a stale file. It also emits
  `drip service start`, because installing a service does not start it.
- **The Windows script is one command per line.** `&&` needs PowerShell 7 and
  these hosts are often on Windows PowerShell 5.1; the Unix script chains with
  `&&` so a failure stops the sequence instead of half-configuring the machine.
- **Reserving a TCP port does not check the server's port range.** The panel has
  never done this — `Deployment` does not carry `tcp_port_min`/`max` — so a port
  outside it is accepted here and refused at registration. Fix it in both paths
  or neither.

## TLS

`tls_mode` picks one of three paths, and `ResolveTLSMode` infers it when unset so
pre-existing config files keep working:

| Mode | Certificate source | Inferred when |
|---|---|---|
| `none` | none; a reverse proxy terminates TLS | nothing else is configured |
| `manual` | `tls_cert` / `tls_key` on disk | a cert pair or `tls_enabled` is set |
| `acme` | certmagic, DNS-01 wildcard | `acme.dns_provider` is set |

ACME mode issues one certificate covering the server domain, the tunnel domain
and `*.<tunnel_domain>` — the wildcard is the whole point, since tunnel
subdomains are unpredictable. DNS-01 is the only challenge that can issue a
wildcard, so provider API credentials are mandatory; HTTP-01 and TLS-ALPN are
explicitly disabled rather than left to fail on a server that may not own port
80 or 443.

Adding a DNS provider is one import plus one entry in the `dnsProviders`
registry in `tls/dns.go`; it only has to satisfy `libdns.RecordAppender` and
`libdns.RecordDeleter`. Cloudflare (upstream `libdns/cloudflare`) and Hostinger
(`tls/hostinger`, written here because no libdns provider exists) ship today.
Weigh the dependency tree before adding heavy SDKs (Route53 pulls all of
aws-sdk-go-v2).

`tls/hostinger` bridges an RRset-shaped API onto libdns: entries are
(name, type, ttl) tuples holding a list of contents, `PUT` takes an `overwrite`
flag that maps onto append versus set, and `DELETE` only filters by
(name, type). Exact-value deletes are therefore a read-modify-write under a
mutex — see the comments there before changing it.

**The API writes and reads values asymmetrically.** Send a TXT content raw and
it is stored and served correctly, but every read echoes it back in zone-file
presentation form: wrapped in double quotes, inner quotes and backslashes
escaped. Verified against the live API — a 43-character ACME digest comes back
as 45 characters while `dig` shows the correct unwrapped value. Every content
read from the API therefore goes through `decodeContent`, including the
survivors of a partial delete, which would otherwise be stored doubly quoted.
Without it `DeleteRecords` matched nothing and returned no error, so ACME
challenge records accumulated until Let's Encrypt refused the authorization.

**Challenge records take the shortest TTL the API allows, never a friendlier
default.** Hostinger refuses anything below 60 seconds, and certmagic asks for
0 — libdns for "do not cache". The server obtains one certificate per name
(`generateCSR` in certmagic takes a single name, so there is no SAN grouping to
reach for), which means the domain and the wildcard solve DNS-01 at the same
`_acme-challenge` name in separate, sequential orders. The CA caches the first
order's answer for the record's TTL, so the second validation reads the first
token unless the two are separated by longer than that. `dnsProviderEntry`
carries a per-provider `propagationDelay` for exactly that gap: 90s for
Hostinger, none for Cloudflare. `acme.propagation_delay_seconds` overrides it.

All three modes build on `baseTLSConfig()`: TLS 1.3 only, three cipher suites,
no ALPN advertisement. ACME mode deliberately layers certmagic's
`GetCertificate` onto that base rather than using certmagic's own `TLSConfig()`,
which would advertise `h2` and `acme-tls/1` and silently change what the
listener negotiates. Keep that property — there is a test asserting the two
modes share a posture.

## Windows service

`drip service install|uninstall|start|stop|restart|status|run` (`cli/service.go`
plus the `_windows.go` files) registers the client with the service control
manager; `cli/tunnel_supervisor.go` is the headless runner it drives, and it is
cross-platform.

- **The supervisor is not `runTunnelWithUI`.** It draws nothing, retries transport
  failures forever with jittered backoff, and only gives up on errors retrying
  cannot fix. `drip http` and `drip start` still use the TUI runner; the two share
  `buildConnectorConfig` and the error classifiers, nothing else.
- **`--config` is mandatory in the service command line.** A service runs as
  LocalSystem, whose home is `C:\Windows\system32\config\systemprofile`, so
  `os.UserHomeDir()` can never reach the config a human wrote. `install` copies it
  to `%ProgramData%\drip\config.yaml`, restricts the DACL to SYSTEM and
  Administrators, and bakes the path into the service arguments; the service also
  exports `DRIP_CONFIG` so fallback paths agree with it.
- **The config is read inside `Execute`, after `svc.Run`.** Failing before the
  dispatcher starts surfaces as a start timeout (error 1053) instead of the real
  error; failing inside it returns exit code 1 and triggers the recovery actions.
- **"Subdomain already taken" is fatal only before the first connect.** After a
  drop the server can still hold the old session for a few seconds, and treating
  that as fatal would leave a service permanently down over its own stale session.
- **Logging goes through `utils.InitFileLogger`, not `zap.Config.OutputPaths`.**
  zap parses output paths as URLs and rejects `C:\...` as an unknown scheme.
- **The service needs a tunnel in the config, and a reservation is not one.**
  The allocation on the server decides the public name; `tunnels:` decides the
  local port behind it, and `selectTunnels` refuses to install a service that
  has nothing to run. `drip config tunnel add|list|remove` writes that half
  without hand-editing YAML, and both installers prompt for it — the PowerShell
  one blocks service registration until the config names a tunnel, since
  otherwise `-InstallService` fails on every clean install.
  `config tunnel list --names` is the scriptable form the installers read.
- **The machine-wide config copy is kept, not refreshed.** `prepareServiceConfig`
  returns an existing `%ProgramData%\drip\config.yaml` untouched, because an
  administrator may have curated it — so a copy left behind by an earlier failed
  install outlives the user config it was seeded from. `--reseed` overwrites it,
  the installer passes that flag when the same run wrote the user config, and
  errors about it name the file actually read. `selectTunnels` takes the config
  path for exactly that reason: reporting the default path while reading the
  machine copy sends the reader to the wrong file.
- **`scripts/install-client.ps1` is the Windows installer**; the bash installers
  cannot register a service and only run under Git Bash or MSYS. It can install
  the service in the same pass (`-InstallService`), and it stops a running
  service before replacing the binary, because Windows will not let an open
  executable be overwritten.

## Installers

`scripts/install.sh` (wrapper), `install-client.sh`, `install-server.sh` and
`install-client.ps1` all default to **this fork**, `nickolasdeluca/drip-ex`,
overridable with `GITHUB_REPO=` or `-Repo`. They install from GitHub Releases, so
a change to the client only reaches users after a tagged release.

`.goreleaser.yaml` pins `project_name: drip` so assets stay
`drip_<version>_<os>_<arch>.tar.gz` whatever the repository or checkout directory
is called — `install-client.sh` builds that file name literally, and the
PowerShell installer matches on the platform and architecture within it.

`install-server.sh` offers managed mode at setup: it writes `db_path`,
`require_auth` and `admin_address`, optionally `admin_public` and
`reservations_only`, and **omits `token:` unless the operator opts into a shared
token**. That default is the point — a client on a shared token registers with no
account, so it can never bind a reservation, and a server installed with one
silently cannot use the panel it just installed. The panel stays on loopback
because first-run setup is unauthenticated by necessity; the installer prints the
SSH tunnel to reach it. `uninstall.sh` warns before removing `/var/lib/drip` when
a control plane database is there — accounts, credentials and allocations cannot
be recreated from the config.

All four verify the SHA-256 from `drip_<version>_checksums.txt` before installing
and **fail closed**: a missing checksum file, an archive the file does not list,
or a mismatch aborts. Keep it that way — these scripts run as root off a piped
URL. Downloads land in a `mktemp -d` scratch directory removed by an `EXIT` trap,
not in fixed `/tmp` paths.

Both bash installers already handle updates: `check_existing_install` compares
the installed binary against the latest release, prompts, and returns through the
`IS_UPDATE` branch of `main()`, which never touches the configuration.
`install-server.sh` copies the outgoing binary to `drip.previous` first and calls
`rollback_binary` when the new one will not start, so a bad release leaves the
host on the old version rather than down.

## CI and publishing

| Workflow | Trigger | Produces |
|---|---|---|
| `ci.yml` | push to main, every PR | `go test -race`, `go vet`, staticcheck, gosec, govulncheck |
| `release.yml` | tag `v*.*.*` | GoReleaser build published to GitHub Releases |
| `release.yml` | manual dispatch | Snapshot archives as workflow artifacts, nothing published |
| `docker.yml` | manual dispatch only | Nothing by default — see below |

- **No container image is published, deliberately.** Docker is unsupported for
  now, so `docker.yml` has had its push triggers commented out and only runs when
  someone starts it by hand. It still targets GHCR with the built-in
  `GITHUB_TOKEN`; restoring the commented `push:` block is all it takes to turn
  publishing back on. Do not "fix" the workflow by re-adding those triggers.
- **The compose files build the image locally** (`docker compose up -d --build`)
  because no published image exists to pull. They must not point at a registry
  tag that nothing produces.
- **GitHub Packages cannot host loose binaries.** Executables go to Releases,
  which is what the installers read from.
- **`Dockerfile.server`'s builder stage is pinned to `$BUILDPLATFORM`.** CGO is
  off and `GOARCH` comes from `TARGETARCH`, so the arm64 image cross-compiles on
  an amd64 runner. Dropping that pin makes the build run under QEMU, which is
  both slow and needs a setup step the workflow does not have.

## Traps

- **Never enable certmagic's on-demand issuance.** With a wildcard covering
  every tunnel subdomain it buys nothing, and it would let arbitrary SNI values
  drive ACME requests straight into the Let's Encrypt rate limit (50 certs per
  registered domain per week). `NewACME` manages a fixed name set and nothing
  else. If on-demand is ever genuinely needed, it must first check the name
  against the live tunnel manager.
- **ACME issuance is synchronous at startup.** A server that cannot present a
  certificate is useless, so bad DNS credentials must fail the boot rather than
  surface later as handshake errors. Only first issuance pays the propagation
  wait; after that the cert loads from `acme.cache_dir`.
- **`acme.cache_dir` holds the ACME account key and must persist.** A deployment
  that wipes it re-issues from scratch every time and burns rate limit. Mount it
  as a volume; back it up.
- **Use `ca: staging` while testing anything ACME-related.** Production rate
  limits are per registered domain per week and a failing loop exhausts them.
- **Authenticated clients bypass the per-IP tunnel cap and registration rate
  limit** (`tunnel.Manager.RegisterOwned`). Those limits exist to stop anonymous
  abuse; a fleet behind one NAT would otherwise lock itself out. Authenticated
  clients are bounded by their account limit and the global cap instead.
- **`validateMetricsAuth` returns true when the token is empty**
  (`proxy/handler.go`). Fail-open is intentional for `/stats` on a self-hosted
  box, but the admin API must never reuse it — write a fail-closed check.
- **Reservations live in SQLite, never in the manager's shard maps.**
  `shard.used` is cleared on `Unregister`, so anything stored there is freed
  when a client disconnects — the opposite of what a reservation means. The
  manager is consulted only to ask whether a name is *currently live*.
- **Reservation bandwidth participates in a `min()`.** The effective limit is
  the smallest of the server default, the reservation override and the client's
  request, so no party can raise a limit another one set. Keep it that way.
- **The published panel must never serve first-run setup.** `admin_public`
  mounts `admin.Server.PublicHandler`, not `Handler`: it refuses everything
  until an administrator exists and refuses `POST /api/bootstrap` for good.
  Both checks fail closed, a database error included. Config validation keeps
  `admin_address` mandatory alongside it, or a deployment could never bootstrap.
- **Session cookies decide `Secure` per request, not per server**
  (`admin/secureCookieFor`). One `admin.Server` answers both a plain-HTTP
  loopback listener and an HTTPS public mount, so a server-wide flag is wrong
  for one of them. A public mount counts as HTTPS even when this process speaks
  plain HTTP, because the only reason to publish it is a TLS terminator in
  front.
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
