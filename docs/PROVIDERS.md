# Providers Guide

Configure custom Claude API endpoints and alternative providers for The Construct CLI.

## Table of Contents

- [Overview](#overview)
- [Quick Setup](#quick-setup)
- [Supported Providers](#supported-providers)
- [Configuration](#configuration)
- [Environment Variables](#environment-variables)
- [Examples](#examples)
- [Troubleshooting](#troubleshooting)

## Overview

The Construct CLI supports configuring alternative providers for Claude Code. This allows you to:

- Use custom Claude API endpoints
- Switch between different providers (Zai, MiniMax, Kimi, Qwen, Mimo, etc.)
- Configure provider-specific settings (timeouts, models)
- Reference environment variables for API keys

## Quick Setup

### Step 1: Choose a Provider

Select from the [supported providers](#supported-providers) below.

### Step 2: Configure Provider

Edit `~/.config/construct-cli/config.toml`:

```toml
[claude.cc.zai]
ANTHROPIC_BASE_URL = "https://api.z.ai/api/anthropic"
ANTHROPIC_AUTH_TOKEN = "${CNSTR_ZAI_API_KEY}"
```

### Step 3: Set Environment Variable

```bash
export CNSTR_ZAI_API_KEY="your-api-key-here"
```

### Step 4: Run Construct

```bash
construct claude zai "Help me with this code"
```

## Supported Providers

### Z.AI GLM

**Provider ID:** `zai`

```toml
[claude.cc.zai]
ANTHROPIC_BASE_URL = "https://api.z.ai/api/anthropic"
ANTHROPIC_AUTH_TOKEN = "${CNSTR_ZAI_API_KEY}"
API_TIMEOUT_MS = "3000000"
CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC = "1"
```

**Environment variable:** `CNSTR_ZAI_API_KEY`

### MiniMax M2

**Provider ID:** `minimax`

```toml
[claude.cc.minimax]
ANTHROPIC_BASE_URL = "https://api.minimax.io/anthropic"
ANTHROPIC_AUTH_TOKEN = "${CNSTR_MINIMAX_API_KEY}"
API_TIMEOUT_MS = "3000000"
ANTHROPIC_MODEL = "MiniMax-M2"
ANTHROPIC_SMALL_FAST_MODEL = "MiniMax-M2"
```

**Environment variable:** `CNSTR_MINIMAX_API_KEY`

### Moonshot Kimi K2

**Provider ID:** `kimi`

```toml
[claude.cc.kimi]
ANTHROPIC_BASE_URL = "https://api.moonshot.ai/anthropic"
ANTHROPIC_AUTH_TOKEN = "${CNSTR_KIMI_API_KEY}"
API_TIMEOUT_MS = "3000000"
CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC = "1"
```

**Environment variable:** `CNSTR_KIMI_API_KEY`

### Alibaba Qwen

**Provider ID:** `qwen`

```toml
[claude.cc.qwen]
ANTHROPIC_BASE_URL = "https://dashscope-intl.aliyuncs.com/api/v2/apps/claude-code-proxy"
ANTHROPIC_AUTH_TOKEN = "${CNSTR_QWEN_API_KEY}"
API_TIMEOUT_MS = "3000000"
CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC = "1"
```

**Environment variable:** `CNSTR_QWEN_API_KEY`

### Xiaomi Mimo

**Provider ID:** `mimo`

```toml
[claude.cc.mimo]
ANTHROPIC_BASE_URL = "https://api.xiaomimimo.com/anthropic"
ANTHROPIC_AUTH_TOKEN = "${CNSTR_MIMO_API_KEY}"
API_TIMEOUT_MS = "3000000"
```

**Environment variable:** `CNSTR_MIMO_API_KEY`

## Configuration

### Provider Section Format

```toml
[claude.cc.<provider-id>]
ANTHROPIC_BASE_URL = "https://api.example.com/anthropic"
ANTHROPIC_AUTH_TOKEN = "${ENV_VAR_NAME}"
API_TIMEOUT_MS = "3000000"
```

### Configuration Options

**Required:**
- `ANTHROPIC_BASE_URL` - API endpoint URL
- `ANTHROPIC_AUTH_TOKEN` - API authentication key

**Optional:**
- `API_TIMEOUT_MS` - Request timeout in milliseconds (default: 300000)
- `ANTHROPIC_MODEL` - Model to use
- `ANTHROPIC_SMALL_FAST_MODEL` - Small/fast model for quick tasks
- `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC` - Disable non-essential traffic

## Environment Variables

### Variable Naming Convention

**Construct prefix:** `CNSTR_`

**Pattern:** `CNSTR_<PROVIDER>_API_KEY`

**Examples:**
- `CNSTR_ZAI_API_KEY`
- `CNSTR_MINIMAX_API_KEY`
- `CNSTR_KIMI_API_KEY`

### Variable Reference

**In config.toml:**
```toml
[claude.cc.zai]
ANTHROPIC_AUTH_TOKEN = "${CNSTR_ZAI_API_KEY}"
```

**Set in shell:**
```bash
export CNSTR_ZAI_API_KEY="sk-..."
```

**Fallback behavior:**
- If `CNSTR_ZAI_API_KEY` is not set, Construct falls back to `ZAI_API_KEY`
- This allows using either variable name

### Passthrough Variables

Construct automatically forwards common provider keys:

- `ANTHROPIC_API_KEY` - Official Anthropic API
- `OPENAI_API_KEY` - OpenAI API key
- `GEMINI_API_KEY` - Google Gemini API key
- `OPENROUTER_API_KEY` - OpenRouter API key
- `ZAI_API_KEY` - Zai API key
- `OPENCODE_API_KEY` - OpenCode API key
- `HF_TOKEN` - Hugging Face API token
- `KIMI_API_KEY` - Kimi API key
- `MINIMAX_API_KEY` - MiniMax API key
- `MIMO_API_KEY` - Mimo API key
- `QWEN_API_KEY` - Qwen API key

**Prefix handling:** All `CNSTR_` prefixed variables are also auto-forwarded.

## Examples

### Basic Provider Setup

```toml
[claude.cc.zai]
ANTHROPIC_BASE_URL = "https://api.z.ai/api/anthropic"
ANTHROPIC_AUTH_TOKEN = "${CNSTR_ZAI_API_KEY}"
```

```bash
# Set the API key
export CNSTR_ZAI_API_KEY="your-key-here"

# Run with provider
construct claude zai "Help me debug this"
```

### Multiple Providers

```toml
[claude.cc.zai]
ANTHROPIC_BASE_URL = "https://api.z.ai/api/anthropic"
ANTHROPIC_AUTH_TOKEN = "${CNSTR_ZAI_API_KEY}"

[claude.cc.minimax]
ANTHROPIC_BASE_URL = "https://api.minimax.io/anthropic"
ANTHROPIC_AUTH_TOKEN = "${CNSTR_MINIMAX_API_KEY}"
```

```bash
# Use Zai provider
construct claude zai "Task for Zai"

# Use MiniMax provider
construct claude minimax "Task for MiniMax"
```

### Custom API Endpoint

```toml
[claude.cc.custom]
ANTHROPIC_BASE_URL = "https://my-custom-proxy.com/anthropic"
ANTHROPIC_AUTH_TOKEN = "${MY_CUSTOM_API_KEY}"
API_TIMEOUT_MS = "600000"  # 10 minutes
```

### Model Configuration

```toml
[claude.cc.minimax]
ANTHROPIC_BASE_URL = "https://api.minimax.io/anthropic"
ANTHROPIC_AUTH_TOKEN = "${CNSTR_MINIMAX_API_KEY}"
ANTHROPIC_MODEL = "MiniMax-M2"
ANTHROPIC_SMALL_FAST_MODEL = "MiniMax-M2"
```

## Troubleshooting

### Provider Not Found

**Error:** `provider "xyz" not found`

**Solution:** Check provider ID is correct

```bash
# List available providers
construct claude --help

# Check config
construct sys config
```

### API Key Not Working

**Error:** `401 Unauthorized` or `invalid API key`

**Solutions:**

1. **Check environment variable is set**
   ```bash
   echo $CNSTR_ZAI_API_KEY
   ```

2. **Check variable name in config**
   ```toml
   # Correct
   ANTHROPIC_AUTH_TOKEN = "${CNSTR_ZAI_API_KEY}"
   
   # Wrong (no quotes, no braces)
   ANTHROPIC_AUTH_TOKEN = $CNSTR_ZAI_API_KEY
   ```

3. **Verify API key is valid**
   - Check provider dashboard
   - Regenerate key if needed
   - Check key has required permissions

4. **Check for typos in provider name**
   ```bash
   # Correct
   construct claude zai "..."
   
   # Wrong
   construct claude ZAI "..."  # Provider IDs are lowercase
   ```

### Connection Timeout

**Error:** `timeout waiting for response`

**Solutions:**

1. **Increase timeout**
   ```toml
   [claude.cc.zai]
   API_TIMEOUT_MS = "600000"  # 10 minutes
   ```

2. **Check network connectivity**
   ```bash
   # Test API endpoint
   curl -I https://api.z.ai
   ```

3. **Check if API is down**
   - Visit provider status page
   - Check provider social media

### Model Not Available

**Error:** `model "xyz" not found`

**Solutions:**

1. **Check provider supports the model**
   - See provider documentation
   - Check model availability in your region

2. **Verify model name in config**
   ```toml
   ANTHROPIC_MODEL = "correct-model-name"
   ```

3. **Check provider dashboard**
   - Ensure model is enabled
   - Check API has access to model

## Provider Development

### Adding a New Provider

To add a new provider:

1. **Add configuration section**
   ```toml
   [claude.cc.newprovider]
   ANTHROPIC_BASE_URL = "https://api.newprovider.com/anthropic"
   ANTHROPIC_AUTH_TOKEN = "${CNSTR_NEWPROVIDER_API_KEY}"
   ```

2. **Add to help text**
   - Edit `internal/ui/help.go`
   - Add provider to available providers list

3. **Add to post-install verification**
   - Edit `internal/config/packages.go`
   - Add provider slug to verification loop

4. **Update documentation**
   - Add to this guide
   - Update README.md

See [Development Guide](DEVELOPMENT.md) for details.

## Best Practices

### Security

**✅ DO:**
- Use environment variables for API keys
- Never commit API keys to config files
- Use `CNSTR_` prefix for consistency
- Rotate API keys regularly

**❌ DON'T:**
- Hardcode API keys in config.toml
- Commit API keys to version control
- Share config files with API keys
- Use weak or expired API keys

### Configuration

**✅ DO:**
- Test provider configuration before using
- Use appropriate timeouts for your use case
- Configure multiple providers for comparison
- Keep provider configs commented when not in use

**❌ DON'T:**
- Leave unused provider configs active
- Set excessively long timeouts
- Override same provider multiple times
- Mix up provider IDs

## Next Steps

- [Configuration Guide](CONFIGURATION.md) - Complete config reference
- [Security Guide](SECURITY.md) - Provider security best practices
- [Installation Guide](INSTALLATION.md) - Installation instructions

## Getting Help

**Provider-specific issues:**
- Check provider documentation
- Visit provider dashboard
- Check provider status page

**Construct issues:**
- `construct sys doctor` - System health check
- `construct sys config` - Edit configuration
- [GitHub Issues](https://github.com/EstebanForge/construct-cli/issues) - Report bugs
