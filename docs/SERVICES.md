# Services & Integrations Recipes

How to wire the services your agent depends on (memory, search, ticketing, chat, Git, LLM providers, host-side tools) into the Construct sandbox. Each service below is a copy-paste recipe. The mental model comes first; recipes follow.

> See [CONFIGURATION.md](CONFIGURATION.md) -> Host Service Bridging for the full reference on `host_service_env` and `env_passthrough`.

## The mental model: hot vs cold wiring

Every service that reaches the sandbox gets in through one of two channels, and they have **different lifecycles**. Getting this wrong is the single most common cause of "the agent says my service is off".

| Channel | Mechanism | Lifecycle | Typical use |
|---------|-----------|-----------|-------------|
| **Hot** | `env_passthrough` (or the `CNSTR_` prefix) | Injected as `-e` at **every agent launch**. Self-heals on each run. | Secrets, tokens, host-independent values |
| **Cold** | `host_service_env` | Baked into the daemon container at **creation only**. Editing the value regenerates the override file but does **not** update a running container. | Service URLs that must differ inside the sandbox |

**Implication.** Changing a `host_service_env` value requires recreating the daemon:

```bash
construct sys daemon restart
```

`env_passthrough` values need no restart; they are read fresh from your host shell on every agent run.

**Prerequisite for all secrets.** `env_passthrough` reads the **host** environment. Export your secrets in your host shell first (for example by sourcing `~/.secrets` from your shell rc). Construct forwards what is already in the host env; it does not read `~/.secrets` directly.

## Recipe 1: Token-only services (Asana, Slack, GitHub, Jira/Confluence, Context7, Brave)

These services are reached over the public internet; the agent only needs the credential. Use **hot** passthrough. Add each variable name to `env_passthrough`:

```toml
[sandbox]
env_passthrough = [
    "ASANA_ACCESS_TOKEN",       # Asana: asana_* tools
    "SLACK_USER_TOKEN",         # Slack: slack_* tools (user token, not bot)
    "GITHUB_TOKEN",             # GitHub: gh + git auth
    "ATLASSIAN_BASE_URL",       # Jira/Confluence base URL
    "ATLASSIAN_EMAIL",          # Jira/Confluence user email
    "ATLASSIAN_API_TOKEN",      # Jira/Confluence API token
    "CONTEXT7_API_KEY",         # Context7: library docs
    "BRAVE_API_KEY",            # Brave: web search
]
```

Atlassian needs **three** vars (base URL + email + token) because it authenticates with HTTP basic over an email/token pair against a tenant-specific URL.

**No restart needed.** The next agent run picks up the values from your host shell.

### Keeping secrets namespaced: the `CNSTR_` prefix

If you prefer not to pollute your host env with bare names like `OPENAI_API_KEY`, use the prefix channel. Any host var prefixed `CNSTR_` is forwarded after the prefix is stripped: host `CNSTR_OPENAI_API_KEY` becomes `OPENAI_API_KEY` inside the sandbox.

```toml
[sandbox]
env_passthrough_prefixes = ["CNSTR_"]
```

This is the recommended home for LLM provider keys (see Recipe 3).

## Recipe 2: agentmemory (self-hosted, LAN/NAS) — secret + URL

`agentmemory` is a self-hosted memory server. The agent needs **two** pieces of config: a secret (hot) and a URL (cold, because the URL uses a host-only name that does not resolve inside the container).

The trap: on the host you typically reach the server by a friendly name (mDNS hostname, a Tailscale name, etc.). That name resolves on the host but **not** inside the sandbox, so you cannot passthrough the host URL verbatim. Declare a container-routable address instead.

```toml
[sandbox]
# Secret: hot. The value is host-independent, so passthrough is correct.
env_passthrough = ["AGENTMEMORY_SECRET"]

# URL: cold. Use a container-routable address (raw IP), NOT the host hostname.
# localhost/127.0.0.1 are auto-rewritten to host.docker.internal; any other
# host passes through unchanged.
host_service_env = [
    "AGENTMEMORY_URL=http://192.168.10.250:3111",
]
```

After editing `host_service_env`, recreate the daemon so the new URL is baked in:

```bash
construct sys daemon restart
```

Verify the live value inside the container:

```bash
docker exec construct-cli-daemon printenv AGENTMEMORY_URL
# expect: http://192.168.10.250:3111
```

**Why split the two values across channels?** The secret travels fine as a hot passthrough (host-independent). The URL does not, because the host value (e.g. `http://whitebox:3111`) uses a name the container cannot resolve. `host_service_env` lets you declare a different, container-routable URL. Network mode must allow the LAN (default `permissive` does).

**Symptom of a stale URL.** The agent reports agentmemory as "off", and its effective URL is the extension's fallback default (commonly `http://host.docker.internal:<port>`), not your configured value. That means the daemon container predates the current override. Run `construct sys daemon restart`.

## Recipe 3: LLM provider aliases (Claude-compatible endpoints)

Construct supports Claude-compatible provider aliases under `[claude.cc.<name>]`. Each block points `ANTHROPIC_BASE_URL` at a third-party endpoint and authenticates with `ANTHROPIC_AUTH_TOKEN`. Reference host secrets with `${VAR}` syntax so values never live in the config file:

```toml
[claude.cc.zai]
ANTHROPIC_BASE_URL = "https://api.z.ai/api/anthropic"
ANTHROPIC_AUTH_TOKEN = "${CNSTR_ZAI_API_KEY}"
API_TIMEOUT_MS = "3000000"
CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC = "1"

[claude.cc.minimax]
ANTHROPIC_BASE_URL = "https://api.minimax.io/anthropic"
ANTHROPIC_AUTH_TOKEN = "${CNSTR_MINIMAX_API_KEY}"
ANTHROPIC_MODEL = "MiniMax-M2.7"
CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC = "1"
```

The `${CNSTR_*}` refs resolve from the **prefixed** passthrough channel (Recipe 1). Export `CNSTR_ZAI_API_KEY`, `CNSTR_MINIMAX_API_KEY`, etc. in your host shell; the prefix is stripped on the way in, but the provider block reads the original prefixed name.

Invoke a provider as `construct claude <name> "..."`. See [PROVIDERS.md](PROVIDERS.md) for the full provider list and model pinning.

## Recipe 4: Host-side tools (`host_binaries`)

Some CLIs must run on the **host**, not in the sandbox (they touch host state, host-only config, or host services). List them in `host_binaries`; the agent sees them on PATH and a shim proxies each invocation to a host-side bridge.

```toml
[sandbox]
host_binaries = ["wicket"]
```

Each listed binary runs on the host with full container-controlled argv. Only list binaries you trust with that. Requires `construct build` after first enabling. Full details, security model, and the non-interactive caveat: [HOST-EXEC.md](HOST-EXEC.md).

## Recipe 5: Local dev servers (browser -> host)

A headless browser (agent-browser) inside the sandbox cannot reach host dev servers like `http://myapp.localhost` through DNS, because Chromium hardcodes `localhost`/`*.localhost` to `127.0.0.1`. Construct relays those connections instead:

```toml
[sandbox]
host_loopback_ports = [80, 443]  # default; same port both sides
```

Add non-standard ports (e.g. `3000`) as needed. This is for **browser** traffic to host vhosts; non-browser tools (curl, git) already resolve via DNS. See [CONFIGURATION.md](CONFIGURATION.md) -> Host Loopback Forwarding.

## Troubleshooting

| Symptom | Likely cause | Fix |
|---------|--------------|-----|
| Agent says service is "off" / unreachable; effective URL is an `host.docker.internal` default, not your configured value | Daemon container predates the current `host_service_env` override (cold value stale) | `construct sys daemon restart`, then verify with `docker exec construct-cli-daemon printenv <VAR>` |
| Service works on host but agent cannot resolve the hostname | `env_passthrough` forwarded a host-only name (mDNS, Tailscale, `/etc/hosts` alias) the container cannot resolve | Move the URL to `host_service_env` with a container-routable address (raw IP) |
| Token-sensitive tools report unauthorized after you rotated a key | Old token still cached; passthrough is hot but the agent process is long-lived | Restart the agent run; confirm `echo $<VAR>` on the host shows the new value |
| Provider alias (`construct claude <name>`) fails auth | `${CNSTR_*}` ref not exported on host, or prefix channel disabled | Export `CNSTR_<NAME>` in host shell; confirm `env_passthrough_prefixes = ["CNSTR_"]` |
| Headless browser cannot load `http://<vhost>.localhost` | Not a DNS problem; Chromium bypasses DNS for `*.localhost` | Add the port to `host_loopback_ports` (Recipe 5) |

## Replicating this setup

A minimal services-enabled config combines: one `env_passthrough` list for tokens, the `CNSTR_` prefix for provider keys, a `host_service_env` block for any LAN service URL, and optional `host_binaries` + `host_loopback_ports`. Start from the recipes above, export your secrets in the host shell, run `construct sys daemon restart` once after setting `host_service_env`, and verify with `printenv` inside the container.
