# Hide Secrets Mode

**Experimental feature** that prevents LLM agents from seeing raw secrets in your project files and environment variables.

## What It Does

When enabled, Construct creates an isolated workspace where:
- Project files containing secrets are **redacted** (values replaced with `CONSTRUCT_REDACTED_<HASH>`)
- Environment variables with secret names are **masked** for the agent
- The agent **cannot** access raw secrets, only the masked versions
- Your original project files **remain unchanged**

## Security Guarantee

> **Core guarantee**: When hide-secrets mode is enabled, the agent process must not have direct access to raw project secrets by default.

This protects against:
- Agents reading `.env` files and printing secrets
- Agents copying credentials to output
- Accidental leakage in logs and terminal output
- Tools that display environment variables

## Quick Start

### 1. Enable the Feature Gate (Required)

Set the environment variable:
```bash
export CONSTRUCT_EXPERIMENT_HIDE_SECRETS=1
```

### 2. Configure in `config.toml`

```toml
[security]
hide_secrets = true
hide_secrets_mask_style = "hash"
```

### 3. Run Construct Normally

```bash
construct claude "Help me debug my API code"
```

The agent will now see redacted secrets instead of real values.

## Configuration Options

### Basic Settings

```toml
[security]
# Master switch (requires CONSTRUCT_EXPERIMENT_HIDE_SECRETS=1)
hide_secrets = false

# Mask style: "hash" (default) or "fixed"
hide_secrets_mask_style = "hash"

# Emit session report after each run
hide_secrets_report = true

# Hide .git directory (recommended)
hide_git_dir = true
```

### Mask Styles

**Hash (default)**: Unique placeholder per secret
```
Before: DATABASE_URL=postgresql://user:pass123@localhost/db
After:  DATABASE_URL=CONSTRUCT_REDACTED_A1B2C3D4
```
Better for debugging - you can identify which secret was redacted.

**Fixed**: Static placeholder
```
Before: DATABASE_URL=postgresql://user:pass123@localhost/db
After:  DATABASE_URL=CONSTRUCT_REDACTED
```
Maximum secrecy - all secrets look identical.

### Advanced Configuration

#### Force-Scan Specific Files

```toml
# Always scan these files for secrets, even if they don't match heuristics
hide_secrets_deny_paths = [
    "**/secrets.yml",
    "**/.env.local",
    "config/prod/*.yaml"
]
```

#### Exclude Files from Redaction

⚠️ **DANGER**: Files in this list will NOT be redacted - secrets will be visible to agents.

```toml
# Use ONLY for files that tools must read directly
hide_secrets_allow_paths = [
    "~/.aws/credentials",  # AWS CLI needs this
    "config/tool-specific-secrets.yaml"
]
```

**Warning**: This breaks the security model for allowlisted files. Use sparingly.

#### Environment Variable Passthrough

```toml
# These env vars will be visible to agents in raw form
hide_secrets_passthrough_vars = [
    "PUBLIC_API_URL",
    "APP_ENV",
    "NON_SECRET_CONFIG"
]
```

## What Gets Scanned

### Automatically Scanned Files

Construct scans files matching these patterns:
- `.env`, `.env.*`
- `*.pem`, `*.key`, `*.p12`, `*.pfx`, `id_rsa`, `id_ed25519`
- `.npmrc`, `.pypirc`, `.netrc`, `.dockercfg`
- `*.tfvars`, `*.tfvars.json`
- `application*.yml`, `application*.yaml`, `application*.properties`
- `config*.json`, `config*.yaml`, `config*.yml`, `config*.toml`, `*.ini`

### Secret Indicators

Files are scanned for keys containing:
- `api_key`, `apikey`, `api-key`
- `secret`, `password`, `passwd`, `pwd`
- `token`, `auth`, `authorization`
- `dsn`, `connection_string`, `connection-string`
- `private_key`, `private-key`, `privatekey`
- `credential`, `cert`, `certificate`

### Provider-Specific Patterns

Construct also detects:
- GitHub tokens: `ghp_`
- OpenAI keys: `sk-`
- Anthropic keys: `sk-ant-`
- AWS keys: `AKIA`
- Stripe keys: `sk_live_`/`pk_live_`
- Slack tokens: `xoxb-`
- Private key blocks: `-----BEGIN [A-Z]+ PRIVATE KEY-----`

## Use Cases

### ✅ Good Use Cases

**Debugging API code** without exposing API keys
```bash
construct claude "Debug why my API calls are failing"
```

**Code review** of authentication logic
```bash
construct claude "Review my authentication flow for security issues"
```

**Working with sensitive projects**
```bash
construct claude "Help me refactor this database migration code"
```

### ❌ When NOT to Use

**When tools need direct secret access**
- AWS CLI (`~/.aws/credentials`)
- kubectl (`~/.kube/config`)
- Tools that read credentials files directly

Use `hide_secrets_allow_paths` for these files, or disable hide-secrets mode entirely.

**When agents need to make authenticated API calls**
- This is handled via the provider proxy (future enhancement)
- For now, use standard mode for workflows requiring API calls

## Session Behavior

### Read-Only Sessions

In hide-secrets mode:
- Agent runs in an **isolated workspace** (OverlayFS on Linux, APFS clones on macOS)
- Agent writes **do not persist** to your real project
- Changes remain in session workspace only
- On session end, you'll see a warning if there are changes

Example output:
```
⚠️  Hide-secrets session ended with changes
   Files changed: 3
   Session directory: ~/.config/construct-cli/security/sessions/abc123
   Changes were NOT written to the real project.
   Review changes in the session directory.
```

### Session Cleanup

Sessions are automatically cleaned up:
- **Unchanged sessions**: Deleted immediately
- **Changed sessions**: Retained for manual review
- **Crashed sessions**: Detected via PID checks and cleaned after grace period

## Example Workflows

### Debugging with Secrets

```bash
# 1. Enable hide-secrets mode
export CONSTRUCT_EXPERIMENT_HIDE_SECRETS=1

# 2. Run agent
construct claude "Debug my database connection code"

# Agent sees:
# DATABASE_URL=CONSTRUCT_REDACTED_A1B2C3D4
# API_KEY=CONSTRUCT_REDACTED_D4E5F6A7

# Your actual .env file:
# DATABASE_URL=postgresql://user:realpass@localhost/db
# API_KEY=sk-1234567890abcdef
```

### Working with AWS CLI

```toml
[security]
hide_secrets = true
# Allow AWS CLI to access credentials
hide_secrets_allow_paths = ["~/.aws/credentials"]
```

```bash
construct claude "Use AWS CLI to list S3 buckets"
# Agent can run: aws s3 ls
# But other secrets remain redacted
```

### Multi-Environment Projects

```toml
[security]
hide_secrets = true
hide_secrets_deny_paths = [
    "config/prod/*.yaml",
    "config/staging/*.yaml"
]
```

## Troubleshooting

### Feature Not Working

**Check**: Is the environment gate set?
```bash
echo $CONSTRUCT_EXPERIMENT_HIDE_SECRETS
# Should output: 1
```

**Check**: Is `hide_secrets = true` in config?
```bash
construct sys doctor | grep hide_secrets
```

### Tools Failing in Hide-Secrets Mode

**Problem**: AWS CLI, kubectl, or similar tools failing
```
aws: Unable to locate credentials
```

**Solution**: Add credentials file to allowlist
```toml
hide_secrets_allow_paths = ["~/.aws/credentials"]
```

### Too Many False Positives

**Problem**: Non-secret values being redacted

**Solution**: Use passthrough for specific env vars
```toml
hide_secrets_passthrough_vars = ["PUBLIC_API_URL", "APP_NAME"]
```

### Session Changes Not Persisting

**This is intentional behavior** in hide-secrets mode.

**To persist changes**:
1. Review changes in session directory
2. Manually copy desired changes to your project
3. Or disable hide-secrets mode for that workflow

## Security Best Practices

### ✅ DO

- Use hide-secrets mode when debugging or reviewing code
- Keep `.git` hidden (`hide_git_dir = true`)
- Use hash mask style for easier debugging
- Review allowlist entries carefully
- Keep CONSTRUCT_EXPERIMENT_HIDE_SECRETS=1 env-only (don't add to shell profiles)

### ❌ DON'T

- Add all config files to allowlist (defeats the purpose)
- Use `hide_secrets_allow_paths` for files you don't control
- Commit actual secrets to git (even with hide-secrets enabled)
- Assume hide-secrets mode prevents all secret leaks (defense-in-depth)

## Limitations

### What's Protected (V1)

- ✅ Project files with secret values
- ✅ Environment variables with secret names
- ✅ Output from Construct-controlled subprocesses
- ✅ `.git` directory (when `hide_git_dir = true`)

### What's NOT Protected (V1)

- ❌ Secrets already in git history
- ❌ Binary/archive files (not scanned)
- ❌ Provider API calls (requires proxy feature, V2)
- ❌ Malicious host-side tools outside Construct
- ❌ Subprocess output from non-Construct tools

### Future Enhancements (V2+)

- Provider proxy for authenticated API calls
- Daemon isolation for stronger security boundaries
- Advanced proxy policy controls
- Organization-level policy presets

## Technical Details

### Workspace Isolation

**Linux**: Uses OverlayFS
```
Lower layer (real project, read-only)
  ├── .env          ← Contains real secrets
  └── src/          ← Regular files

Upper layer (session-specific, writable)
  ├── .env          ← Redacted copy (CONSTRUCT_REDACTED_*)
  └── changes/      ← Agent writes

Merged view (agent workspace)
  ├── .env          ← Shows redacted version
  └── src/          ← Passthrough from lower
```

**macOS**: Uses APFS clones (copy-on-write) with similar isolation

### Audit Logging

Security events are logged to:
```
~/.config/construct-cli/security/audit/security-audit.log
```

Events include:
- Session start/end
- Token mint/revoke
- Redaction operations
- Policy violations

View audit logs (V2):
```bash
construct sys security audit verify
```

## Getting Help

- **Issues**: Report bugs at github.com/EstebanForge/construct-cli/issues
- **Security concerns**: Use private security disclosure
- **Feature requests**: Welcome via GitHub issues

## See Also

- [Implementation Plan](SECRETS-REDACTION.md) - Technical architecture
- [Architecture Design](ARCHITECTURE-DESIGN.md) - Overall system architecture
- [Configuration Guide](README.md) - General configuration
