# Agents Reference

Complete list of supported AI agents in The Construct CLI.

## Overview

The Construct CLI supports 15+ AI agents that can run inside an isolated sandboxed container. Each agent has its own strengths and use cases.

## Available Agents

### Claude Code (`claude`)

**Provider:** Anthropic (official)
**Strengths:** Full-code agent with strong editing/refactoring capabilities
**Best for:** General coding, code review, refactoring, debugging

```bash
construct claude "Help me refactor this function"
construct claude "Review this code for security issues"
```

**Documentation:** [docs.anthropic.com](https://docs.anthropic.com)

### Gemini CLI (`gemini`)

**Provider:** Google
**Strengths:** Multimodal capabilities, strong reasoning
**Best for:** Code explanation, documentation, multi-language support

```bash
construct gemini "Explain how this code works"
construct gemini "Generate documentation for this API"
```

### Qwen Code (`qwen`)

**Provider:** Alibaba
**Strengths:** Tuned for coding, fast responses
**Best for:** Quick code generation, debugging, code completion

```bash
construct qwen "Help me debug this error"
construct qwen "Complete this function"
```

### GitHub Copilot CLI (`copilot`)

**Provider:** GitHub
**Strengths:** GitHub integration, context-aware suggestions
**Best for:** Everyday coding tasks, quick suggestions

```bash
construct copilot "Suggest a completion for this function"
construct copilot "Generate tests for this code"
```

### Crush CLI (`crush`)

**Provider:** Charmbracelet
**Strengths:** CLI tool development, Rust ecosystem
**Best for:** Building CLI tools, systems programming

```bash
construct crush "Help me build a CLI tool"
construct crush "Optimize this Rust code"
```

### Pi Coding Agent (`pi`)

**Provider:** General-purpose
**Strengths:** Extensible tooling, balanced capabilities
**Best for:** General coding tasks, prototyping

```bash
construct pi "Help me implement this feature"
construct pi "Debug this issue"
```

### Oh My Pi (`omp`)

**Provider:** Fork of Pi with enhancements
**Strengths:** Python/IPython integration, LSP support, extended tooling
**Best for:** Python development, data science, Jupyter workflows

```bash
construct omp "Analyze this Python code"
construct omp "Help with this pandas script"
```

### Amp CLI (`amp`)

**Provider:** Amp
**Strengths:** Amp agent CLI
**Best for:** Amp-specific workflows

```bash
construct amp "Help me with this task"
```

### OpenCode (`opencode`)

**Provider:** General-purpose
**Strengths:** General code assistant
**Best for:** Code explanation, general programming help

```bash
construct opencode "Explain this architecture"
construct opencode "Help me understand this code"
```

### Cline (`cline`)

**Provider:** Cline
**Strengths:** Agentic workflow helper
**Best for:** Multi-step tasks, workflow automation

```bash
construct cline "Help me set up this project"
construct cline "Implement this feature end-to-end"
```

### Droid CLI (`droid`)

**Provider:** Factory Droid
**Strengths:** Droid agent CLI
**Best for:** Droid-specific workflows

```bash
construct droid "Help with this Android development task"
```

### Goose CLI (`goose`)

**Provider:** Block Goose
**Strengths:** Block Goose agent CLI
**Best for:** Goose-specific workflows

```bash
construct goose "Help me with this task"
```

### Kilo Code CLI (`kilocode`)

**Provider:** Kilocode
**Strengths:** Agentic coding with modes and workspace targeting
**Best for:** Complex coding tasks, mode-specific operations

```bash
construct kilocode "Refactor this component"
construct kilocode "Review this pull request"
```

### OpenAI Codex (`codex`)

**Provider:** OpenAI (legacy)
**Strengths:** Codex-style code generation
**Best for:** Quick code snippets, boilerplate generation

```bash
construct codex "Generate a function that does X"
construct codex "Complete this code pattern"
```

## Provider-Specific Agents

Construct also supports alternative providers for Claude Code:

### Zai GLM (`zai`)

**Provider:** Z.AI
**Best for:** Cost-effective Claude-compatible API
```bash
construct claude zai "Help me with this code"
```

**Configuration:** See [Providers Guide](PROVIDERS.md)

### MiniMax M2 (`minimax`)

**Provider:** MiniMax
**Best for:** Fast responses, cost-effective
```bash
construct claude minimax "Quick code review"
```

### Moonshot Kimi K2 (`kimi`)

**Provider:** Moonshot
**Best for:** Chinese-language support, long context
```bash
construct claude kimi "分析这个代码"  # Chinese input
```

### Alibaba Qwen (`qwen`)

**Provider:** Alibaba
**Best for:** Chinese market, local deployment
```bash
construct claude qwen "帮我调试这个"  # Chinese input
```

### Xiaomi Mimo (`mimo`)

**Provider:** Xiaomi
**Best for:** Chinese-language support, cost-effective
```bash
construct claude mimo "帮助我优化代码"  # Chinese input
```

## Running Agents

### Basic Usage

```bash
# Run default agent
construct claude "Help me with this code"

# Run specific agent
construct gemini "Explain this function"
```

### With Network Modes

```bash
# Strict network isolation
construct claude -ct-n strict "Review my code"

# Offline mode
construct gemini --ct-network offline "Explain this code"
```

### With Configuration

```bash
# Use specific provider
construct claude zai "Debug this API"

# With custom timeout
construct claude --timeout 600000 "Long-running task"
```

## Agent Comparison

| Agent | Best For | Speed | Cost | Special Features |
|-------|-----------|-------|------|------------------|
| **claude** | General coding | Medium | High | Strong refactoring |
| **gemini** | Multimodal | Fast | Medium | Google integration |
| **qwen** | Quick tasks | Fast | Low | Tuned for coding |
| **copilot** | Everyday coding | Fast | High | GitHub integration |
| **omp** | Python dev | Medium | Medium | IPython/Jupyter |
| **pi** | General purpose | Medium | Low | Extensible |

## Choosing an Agent

### By Use Case

**General development:**
- `claude` - Best all-around choice
- `gemini` - Great for explanation
- `qwen` - Fast for quick tasks

**Web development:**
- `claude` - Strong refactoring
- `copilot` - Everyday coding
- `gemini` - Documentation

**Python development:**
- `omp` - Python/IPython integration
- `claude` - General Python support

**Systems programming:**
- `crush` - Rust/CLI tools
- `claude` - Systems programming

**Quick prototyping:**
- `qwen` - Fast responses
- `pi` - Quick iteration

### By Provider

**Anthropic Claude (official):**
- Best Claude quality
- Full feature support
- Higher cost

**Alternative providers:**
- Cost-effective alternatives
- Regional availability
- Specific features (e.g., Chinese language support)

**See:** [Providers Guide](PROVIDERS.md) for configuration

## Agent Configuration

### Global Agent Settings

```toml
[agents]
yolo_all = false              # Run all agents without confirmation
yolo_agents = ["claude"]    # Bypass confirmation for specific agents
clipboard_image_patch = true # Enable image paste support
```

### AGENTS.md Rules

Global agent rules managed via:

```bash
construct sys agents-md
```

This opens your global AGENTS.md file for editing.

**See:** [Global AGENTS.md Rules Management](../README.md) for details.

## Troubleshooting

### Agent Not Found

**Error:** `agent "xyz" not found`

**Solution:**
```bash
# List available agents
construct --help

# Check if agent is installed
construct sys doctor
```

### Agent Not Starting

**Error:** `Failed to start agent`

**Solutions:**

1. **Check agent is installed**
   ```bash
   construct sys update
   ```

2. **Check container is built**
   ```bash
   construct sys doctor
   ```

3. **Check configuration**
   ```bash
   construct sys config
   ```

### Provider Issues

**Error:** `Provider API error`

**Solutions:**

1. **Check API key is set**
   ```bash
   echo $ANTHROPIC_API_KEY
   ```

2. **Check provider configuration**
   ```bash
   construct sys config
   ```

3. **Check provider status**
   - Visit provider status page
   - Check provider documentation

**See:** [Providers Guide](PROVIDERS.md) for details.

## Next Steps

- [Providers Guide](PROVIDERS.md) - Configure custom providers
- [Configuration Guide](CONFIGURATION.md) - Agent settings
- [Installation Guide](INSTALLATION.md) - Setup instructions

## Getting Help

**Agent issues:**
- Check agent documentation
- Visit provider websites
- [GitHub Issues](https://github.com/EstebanForge/construct-cli/issues)

**Construct issues:**
```bash
construct sys doctor    # System health check
construct sys config    # Edit configuration
construct --help         # Show all options
```
