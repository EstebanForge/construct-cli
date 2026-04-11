# Security Guide

Complete security documentation for The Construct CLI, including container security, secret redaction, and best practices.

## Table of Contents

- [Security Overview](#security-overview)
- [Container Security](#container-security)
- [Secret Redaction](#secret-redaction)
- [Security Best Practices](#security-best-practices)
- [Security Expectations](#security-expectations)
- [Build Integrity](#build-integrity)
- [Troubleshooting](#troubleshooting)

## Security Overview

The Construct CLI provides multiple layers of security:

1. **Container Isolation**: Agents run in isolated containers
2. **Network Isolation**: Optional network modes (permissive/strict/offline)
3. **Secret Redaction**: Experimental feature to hide secrets from agents
4. **Ephemeral Containers**: Clean slate on every run
5. **No Path Escape**: Agents cannot access files outside project root
6. **Build Verification**: Cryptographic verification of releases

## Container Security

### Container User

**Default password:** `construct`

**Purpose:** Allows sudo access when running interactive commands

**Security implications:**
- ⚠️ **Warning**: If you expose container to untrusted networks (port forwarding, bridge mode), change the password

**Change password:**
```bash
construct sys set-password
```

### Container Isolation

**What agents cannot do:**
- Access files outside the project directory
- Escape the container filesystem
- Access host system processes (by default)
- Persist data across runs (without explicit configuration)

**What agents CAN do:**
- Read/write project files
- Make network requests (subject to network mode)
- Execute commands within container
- Access mounted volumes (home, SSH agent, etc.)

### User Context

**Default behavior:** Run commands as `construct` user inside container

**Alternative:** Run as host user (when possible)
```toml
[sandbox]
exec_as_host_user = true
```

**Benefits:**
- File ownership matches host user
- Better integration with host permissions
- Prevents root-owned files in project

## Secret Redaction

### Overview

**Experimental feature** that prevents LLM agents from seeing raw secrets in project files and environment variables.

**What gets protected:**
- ✅ Project files with secrets (`.env`, config files, credentials)
- ✅ Environment variables with secret names
- ✅ Agent output from Construct-controlled subprocesses
- ✅ `.git` directory (when enabled)

**What's NOT protected (V1):**
- ❌ Secrets already in git history
- ❌ Binary/archive files
- ❌ Provider API calls (requires proxy, V2)
- ❌ Malicious host-side tools outside Construct

### Enablement

**Required:** Set environment gate
```bash
export CONSTRUCT_EXPERIMENT_HIDE_SECRETS=1
```

**Configure:** Enable in `config.toml`
```toml
[security]
hide_secrets = true
hide_secrets_mask_style = "hash"
```

### How It Works

1. **Workspace Isolation**: Agent runs in isolated workspace (OverlayFS/APFS)
2. **File Redaction**: Secrets replaced with `CONSTRUCT_REDACTED_<HASH>`
3. **Env Masking**: Secret env vars masked before injection
4. **Read-Only Sessions**: Agent writes don't persist to real project

### Configuration Options

```toml
[security]
hide_secrets = true                    # Master switch
hide_secrets_mask_style = "hash"       # hash | fixed
hide_secrets_deny_paths = []           # Force-scan these files
hide_secrets_allow_paths = []          # Never redact these files (dangerous!)
hide_secrets_passthrough_vars = []      # Never mask these env vars
hide_secrets_report = true              # Show scan report
hide_git_dir = true                     # Hide .git directory
```

**Allowlist caution:** Files in `hide_secrets_allow_paths` will NOT be redacted. Use sparingly.

### Use Cases

**✅ Good for:**
- Debugging API code without exposing API keys
- Code review of authentication logic
- Working with sensitive projects

**❌ NOT for:**
- Workflows requiring direct API calls (agent needs real keys)
- Tools that read credentials files (AWS CLI, kubectl)
- Use `hide_secrets_allow_paths` for specific files or disable mode

**Complete guide:** [Hide Secrets Mode](HIDE-SECRETS.md)

## Security Best Practices

### ✅ DO

**1. Use Network Isolation**
```toml
[network]
mode = "strict"  # For sensitive work
allowed_domains = ["*.anthropic.com"]
```

**2. Enable Secret Redaction**
```bash
export CONSTRUCT_EXPERIMENT_HIDE_SECRETS=1
```

**3. Mount Home Selectively**
```toml
[sandbox]
mount_home = false  # Default is more secure
```

**4. Review Allowlists**
```bash
# Check what you're allowing through
construct sys doctor
```

**5. Keep Construct Updated**
```bash
construct sys self-update
```

**6. Use Provider Keys Securely**
```toml
[claude.cc.custom]
ANTHROPIC_AUTH_TOKEN = "${CUSTOM_API_KEY}"  # Reference env var
```

### ❌ DON'T

**1. Expose Container to Untrusted Networks**
```bash
# Avoid port forwarding to public internet
construct --publish 8080:80  # Risky on public networks
```

**2. Add All Config Files to Allowlist**
```toml
[security]
# DON'T DO THIS - defeats the purpose
hide_secrets_allow_paths = ["config/*", ".env*"]
```

**3. Commit Real Secrets to Git**
- Even with hide-secrets enabled, git history contains secrets
- Use environment variables instead
- Use secret management tools (1Password, Vault, etc.)

**4. Use `yolo_all = true` in Untrusted Environments**
```toml
[agents]
yolo_all = true  # Agents run without confirmation
```

**5. Ignore Security Warnings**
```bash
# Pay attention to warnings about:
# - Insecure runtime detection
# - Configuration validation failures
# - Security feature limitations
```

## Security Expectations

### What Construct Secures

**Filesystem isolation:**
- ✅ Agents cannot escape project directory
- ✅ No access to host system files (unless mounted)
- ✅ Temporary filesystem (ephemeral containers)

**Network isolation:**
- ✅ Optional network modes (permissive/strict/offline)
- ✅ Domain/IP allowlists and blocklists
- ✅ Configurable per-command network modes

**Secret protection (with hide-secrets):**
- ✅ Project files redacted before agent sees them
- ✅ Environment variables masked
- ✅ Stream output masked for subprocesses
- ✅ `.git` directory hidden

### What Construct Does NOT Secure

**Agent behavior:**
- ❌ Construct cannot prevent agents from making malicious API calls
- ❌ Cannot prevent agents from exploiting vulnerabilities in called services
- ❌ Cannot prevent agents from exfiltrating data through allowed channels

**Host security:**
- ❌ Does not secure your host machine
- ❌ Does not protect against host-side malware
- ❌ Does not prevent direct access to your files (outside Construct)

**Provider security:**
- ❌ Does not secure your API keys
- ❌ Does not prevent API key theft if you expose them
- ❌ Does not validate provider security practices

**User responsibility:**
- 🔒 Keep your API keys secure
- 🔒 Use strong, unique passwords
- 🔒 Enable 2FA on provider accounts
- 🔒 Review agent code before running (if untrusted)
- 🔒 Don't run untrusted agents with sensitive data
- 🔒 Keep Construct updated

### Shared Responsibility Model

**Construct secures:**
- Container isolation
- Network boundaries
- File system access
- Secret visibility (with hide-secrets)

**You secure:**
- Your API keys and credentials
- Your host machine
- Your provider accounts
- Your network infrastructure
- Your data classification

## Build Integrity

### Verified Builds

**All releases built via GitHub Actions CI/CD:**
- ✅ Automated builds prevent tampering
- ✅ No manual builds
- ✅ Reproducible builds traceable to source commits
- ✅ Comprehensive testing on every build
- ✅ SHA256 checksums for verification

### Verify Downloads

**Always verify release artifacts:**

```bash
# Download checksum from release notes
# Compare with downloaded binary
sha256sum construct

# Should match checksum in release notes
```

**Download from official sources only:**
- [GitHub Releases](https://github.com/EstebanForge/construct-cli/releases)
- Homebrew (official tap)
- Official install scripts

## Troubleshooting

### Security Warnings

**"Container running with default password"**

**Issue:** Security scanner detected default container password

**Solution:** Change container password
```bash
construct sys set-password
```

**"Hide-secrets mode is experimental"**

**Issue:** Feature is experimental and may have limitations

**Solution:** Understand limitations before use
- Read [Hide Secrets Guide](HIDE-SECRETS.md)
- Use in development environments first
- Report issues via GitHub Issues

**"Allowlisted files will be visible to agents"**

**Issue:** Files in `hide_secrets_allow_paths` bypass redaction

**Solution:** Review allowlist entries
```bash
# Check what's allowlisted
construct sys doctor

# Remove unnecessary allowlist entries
# Edit ~/.config/construct-cli/config.toml
```

### Security Auditing

**Check your security posture:**

```bash
# Run security diagnostics
construct sys doctor

# Check what's mounted
construct --mount | grep -v construct

# Review configuration
construct sys config
```

**Review audit logs:**
```bash
# Hide-secrets audit logs (V2)
construct sys security audit verify

# Session reports
ls ~/.config/construct-cli/security/sessions/
```

## Reporting Security Issues

**Found a security vulnerability?**

1. **Do NOT open a public GitHub issue**
2. **Send responsible disclosure** via:
   - GitHub Security Advisory (private)
   - Email to project maintainers
3. **Include:**
   - Vulnerability description
   - Steps to reproduce
   - Impact assessment
   - Suggested fix (if known)

**Response timeline:**
- Acknowledgment within 48 hours
- Fix timeline based on severity
- Public disclosure after fix is released

## Security References

- [Hide Secrets Mode](HIDE-SECRETS.md) - Experimental secret redaction
- [Installation Guide](INSTALLATION.md) - Secure installation practices
- [Configuration Guide](CONFIGURATION.md) - Security configuration options
- [Architecture Design](ARCHITECTURE-DESIGN.md) - Security architecture

## Next Steps

- Review your current security setup
- Enable appropriate security features for your use case
- Keep Construct and dependencies updated
- Follow security best practices

## Getting Help

**Security questions:**
- GitHub Issues: [github.com/EstebanForge/construct-cli/issues](https://github.com/EstebanForge/construct-cli/issues)
- Documentation: See [docs/](../)
- Release notes: Check [GitHub Releases](https://github.com/EstebanForge/construct-cli/releases) for security updates
