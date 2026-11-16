# gomon

A rewrite of [Xymon](https://github.com/xymon-monitoring/xymon) in Go. gomon is one binary that contains the server and the web UI. It accepts the Xymon protocol, so existing Xymon clients, ext scripts and tools work without changes. It also adds encrypted agent traffic, native SNMP polling and OIDC single sign-on.

- **Drop-in protocol server.** Listens on 1984 (plaintext) and 1985 (TLS). The TLS port is wire-compatible with upstream Xymon 4.3.32's `xymons://` recipients and per-host client certificates.
- **Client data analysis.** The `cpu`, `disk`, `inode`, `memory`, `procs`, `ports` and `msgs` columns are computed from client messages using your existing **analysis.cfg**. **client-local.cfg** is returned to clients the same way xymond does it.
- **Native network tests.** Replaces xymonnet. Tests come from hosts.cfg tags: `conn`, TCP services, `http(s)://`, `cont;`, `httpstatus;` and `sslcert`. You can import an existing **hosts.cfg**.
- **Native SNMP.** Supports v1, v2c and v3. Polls system info, the IF-MIB interface table (with traffic rates) and custom OIDs with thresholds. No devmon or external collector is needed.
- **Metrics in PostgreSQL** instead of RRD files. Sources: client data, network tests, SNMP, `data HOST.trends` messages, and `name : value` lines in plugin statuses (Xymon's NCV convention).
- **Web UI.** Built with React, Vite, Tailwind v4 and shadcn/ui. It follows [qui](https://github.com/autobrr/qui) in theming and layout, and ships qui's built-in themes.
- **Authentication.** A built-in admin account, OIDC SSO, and API keys.
- **Alerting.** Rules with delay and repeat, acks, maintenance disables, and recovery notices. Channels: Slack, Discord, ntfy, email and generic webhooks.

## Quick start

```sh
docker compose up -d --build
open http://localhost:8080        # create the admin account on first visit
```

PostgreSQL runs as its own container. gomon keeps its config, CA and certificates in the `/data` volume.

To run without Docker:

```sh
make web build                    # needs Go 1.26+ and Node 20.19+; embeds web/dist into bin/gomon
GOMON_DATABASE_DSN=postgres://gomon:gomon@localhost:5432/gomon?sslmode=disable bin/gomon serve
```

On first start gomon writes a commented `config.toml` into its data directory. You can set every option there or through a `GOMON_*` environment variable (see `internal/config/config.go`).

## Pointing existing Xymon clients at gomon

**Unchanged (plaintext).** Set `XYMSRV` to the gomon host. Hosts register themselves the first time they report (`xymon.autoRegister`).

**Encrypted, Xymon 4.3.32+ built with OpenSSL.** This uses upstream's own TLS support (see xymon-tls(7)):

```sh
# xymonclient.cfg
XYMSRV="xymons://gomon.example.com"
XYMON_TLS_CA="/etc/xymon/gomon-ca.pem"        # Settings → Agents & TLS → CA certificate
XYMON_TLS_CERT="/etc/xymon/web01.pem"         # optional: proves the host identity
XYMON_TLS_KEY="/etc/xymon/web01.key"
```

**Encrypted, any older client, BBWin, devmon or custom scripts.** Run the relay on the monitored host and point `XYMSRV` at `127.0.0.1`:

```sh
gomon relay --listen 127.0.0.1:1984 --server xymons://gomon.example.com \
  --ca /etc/gomon/ca.pem --cert /etc/gomon/web01.pem --key /etc/gomon/web01.key
```

**Ext scripts and plugins** keep calling `$XYMON $XYMSRV "status ..."`. gomon also ships a drop-in `xymon` command: symlink the binary as `xymon`, or run `gomon send`. It understands `xymons://` and the `XYMON_TLS_*` variables.

### Certificates

gomon has a built-in CA. It issues the TLS listener's certificate, which names the host's names and IPs plus `xymon.tlsServerNames`. It also issues client certificates whose subjectAltName is the host name:

```sh
gomon cert issue web01.example.com -o /tmp/web01     # or: Settings → Agents & TLS
gomon cert sign web01.csr                            # sign a CSR so the key never leaves the host
```

A client with a verified certificate may report only for the host its certificate names, from any address. A client without one is checked against `xymon.statusSenders`. Turning on `xymon.requireClientCert` refuses TLS clients that present no certificate. To phase out plaintext, set `xymon.listen = "127.0.0.1:1984"` or leave it empty. You can also bring your own certificate with `xymon.tlsCert` and `xymon.tlsKey`; it is reloaded when the file changes.

## Protocol compatibility

| Message | Support |
|---|---|
| `status[+LIFETIME][/group:G]`, `combo`, `extcombo` | Full support. Includes comma hostnames and "Status message received from" relays. |
| `client[/COLLECTOR] HOST.OS CLASS` | Linux, FreeBSD, Darwin, BBWin and PowerShell clients are analysed; other OSes are stored. The client-local.cfg section is returned. |
| `data HOST.trends`, `data HOST.NAME` | Stored as metrics. |
| `query`, `xymondboard` (host/test/color/page filters, `fields=`), `xymondlog`, `hostinfo`, `clientlog`, `config hosts.cfg` | Supported. |
| `disable`/`enable` (including `-1` = until OK), `ack`/`xymondack`, `drop`, `rename`, `notes`, `ping` | Supported. Admin commands are limited to `xymon.adminSenders`. |
| `meta`, `modify`, `schedule`, `download`, `ghostlist`, … | Accepted and ignored. |

Verified against upstream Xymon 4.3.32 built with OpenSSL. Its `xymon` CLI and `xymonclient.sh` (Darwin) were run against gomon over plaintext, over `xymons://` with mutual TLS, and through `gomon relay`.

### analysis.cfg support

You can edit analysis.cfg under Settings → Configuration. It supports:

- Selectors: `DEFAULT`, `HOST=`, `EXHOST=`, `PAGE=`, `EXPAGE=`, `CLASS=`, `EXCLASS=`. Values can be lists or `%regex`.
- Rules: `UP`, `LOAD`, `DISK` and `INODE` (including `IGNORE`), `MEMPHYS`, `MEMSWAP`, `MEMACT`, `PROC` (min/max/color/TEXT), `PORT` (LOCAL/REMOTE/STATE/min/max) and `LOG` (pattern, COLOR, IGNORE).
- `FILE`, `DIR`, `SVC`, `LINECOUNT` and `TIME=` are accepted but **not evaluated yet**.

## SNMP

Add targets under **SNMP** with v1/v2c community or v3 USM credentials (MD5/SHA-1/SHA-2 auth; DES/AES privacy). Each target reports:

- `snmp`: reachability, sysName, sysDescr, uptime, and custom OIDs with warn/crit thresholds. "below" inverts a threshold and "rate" turns a counter into a per-second rate.
- `if`: every interface with traffic in bit/s and error counts. An interface that has an **ifAlias** (description) and is admin-up but oper-down turns the column yellow.
- Any other column you assign custom OIDs to.

## OIDC

```toml
[oidc]
enabled = true
issuer = "https://auth.example.com/application/o/gomon/"
clientId = "gomon"
clientSecret = "…"
redirectUrl = "https://gomon.example.com/api/auth/oidc/callback"
allowedGroups = ["monitoring"]      # optional, read from the "groups" claim
disableBuiltinLogin = false
```

gomon uses the authorization code flow with PKCE and verifies the nonce. Groups are read from the ID token or from userinfo.

## Architecture

```
cmd/gomon          CLI: serve, send (xymon), relay, cert, user, import-hosts
internal/protocol  Xymon message parsing (status/combo/client/data/NCV)
internal/xymond    1984/1985 listeners, sender ACLs, cert-to-host binding, query commands
internal/engine    live state: colors, purple/stale sweep, acks, disables, events, metric batching
internal/analysis  analysis.cfg + client-local.cfg; client data → cpu/disk/memory/procs/ports/msgs
internal/netmon    native network tests (xymonnet replacement)
internal/snmp      SNMP poller
internal/alerts    alert rules and channels
internal/auth      sessions, bcrypt, OIDC, API keys
internal/pki       built-in CA, certificate reload
internal/store     PostgreSQL (pgx), embedded migrations
internal/api       JSON API, SSE live updates, embedded SPA
web/               React + Vite + Tailwind v4 + shadcn/ui, embedded with go:embed
```

The API is `/api/*`. Authenticate with the session cookie or an `X-API-Key` header (Settings → API keys). Live updates stream from `/api/events` as server-sent events.

## Not implemented yet

- Importing history from existing RRD files.
- Alerting configured through `alerts.cfg`. Alerts are configured in the UI instead.
- xymonnet's `dns`, `ldap` (query), `ntp` and `rpc` tests. Only TCP connect, banner and TLS checks are done for services.
- SNMP traps.
- Reports and SLA pages, and the critical systems view.
- The analysis rules listed above as accepted but not evaluated.

## Development

```sh
make dev-db                    # throwaway Postgres in Docker
go run ./cmd/gomon serve       # API on :8080
make dev-web                   # Vite on :5173 with /api proxied to :8080
make test
```

## License

GPL-2.0-or-later, the same as Xymon. The UI themes in `internal/themes/assets` come from qui (GPL-2.0-or-later); see the NOTICE file there.
