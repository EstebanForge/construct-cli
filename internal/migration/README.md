# Migration Package

Handles automatic version migrations for Construct CLI.

## Overview

The migration package provides seamless upgrades between versions by:
- Tracking installed version in `.version` file
- Detecting version changes on startup
- Non-destructively merging configuration files
- Updating container templates with new versions

## How It Works

### Version Tracking

The installed version is stored in `~/.config/construct-cli/.version`:

```
0.4.0
```

On first run after installation or init, this file is created with the current version.

### Migration Detection

During startup for recognized commands/subcommands, `main.go` checks `migration.NeedsMigration()` (invalid/unknown commands and selected self-managed paths such as `sys self-update` and `sys rebuild` are excluded) which:
1. Reads the installed version from `.version` file
2. If no `.version` file exists:
   - Checks if `config.toml` exists
   - If config exists but no version file → treats as upgrade from 0.3.0 (which didn't have version tracking)
   - If no config either → fresh install, no migration needed
3. Compares versions with `constants.Version` (the binary version)
4. Returns `true` if binary version > installed version

### Migration Process

When migration is needed, `migration.CheckAndMigrate()`:

1. **Updates Container Templates** (`updateContainerTemplates`)
   - Replaces all files in `~/.config/construct-cli/container/` with new versions
   - Updates clipboard sync scripts in `~/.config/construct-cli/scripts/`
   - Safe to replace - no user modifications expected here

2. **Merges Packages File** (`mergePackagesFile`)
   - Moves current `packages.toml` to `packages.toml.backup`
   - Writes a fresh `packages.toml` from the embedded template
   - Re-applies user values from the backup only for keys that exist in the template

3. **Regenerates Topgrade Config** (`regenerateTopgradeConfig`)
   - Regenerates `topgrade.toml` from `packages.toml`

4. **Marks Image for Rebuild** (`markImageForRebuild`)
   - Removes the old `construct-box:latest` Docker image
   - Forces rebuild with new Dockerfile on next agent run
   - Tries both docker and podman runtimes
   - Persistent volumes (agents, packages) are preserved

5. **Updates Version File**
   - Writes new version to `.version`
   - Prevents re-running migration on next startup

### Example Migration Output

**Upgrade from 0.3.0 (no version file):**
```
✓ New version detected: 0.3.0 → 0.4.0
→ Running automatic migration...

→ Updating container templates...
  ✓ Container templates updated

→ Merging packages file...
  → Backup saved: ~/.config/construct-cli/packages.toml.backup
  ✓ Packages merged (user settings preserved)

→ Removing old container image...
  ✓ Image marked for rebuild

✓ Migration complete!
  Note: Container image will rebuild on next agent run
```

**Upgrade from 0.4.0 → 0.5.0 (with version file):**
```
✓ New version detected: 0.4.0 → 0.5.0
→ Running automatic migration...

→ Updating container templates...
  ✓ Container templates updated

→ Merging packages file...
  → Backup saved: ~/.config/construct-cli/packages.toml.backup
  ✓ Packages merged (user settings preserved)

→ Removing old container image...
  ✓ Image marked for rebuild

✓ Migration complete!
  Note: Container image will rebuild on next agent run
```

## Manual Migration

Users can manually trigger a migration of configuration and templates using:

```bash
construct sys config --migrate
```

This command:
- Replaces config.toml with the embedded template and re-applies supported user values
- Replaces all container template files with versions from the binary
- Removes the old Docker image to force rebuild
- Updates the `.version` file to match the binary version

**Use cases:**
- Debugging configuration or template issues
- Forcing a resync with the binary after manual config edits
- Testing migration behavior
- Recovering from partial or failed migrations

**Example output:**
```
✓ Refreshing configuration and templates from binary
  This will update config, templates, and mark The Construct image to be rebuild
```

## Testing

Run the migration tests:

```bash
go test ./internal/migration -v
```

### Test Coverage

- `TestCompareVersions`: Verifies semver comparison logic
- `TestDeepMerge`: Validates config merging behavior

## Integration

### In `main.go`

```go
// Check for migrations before loading config.
// Guarded so typos/unknown subcommands do not trigger migration side effects.
if shouldRunMigration(args) && migration.NeedsMigration() {
    if err := migration.CheckAndMigrate(); err != nil {
        fmt.Fprintf(os.Stderr, "Error during migration: %v\n", err)
        os.Exit(1)
    }
}

// Load config (now guaranteed to be up-to-date when migration runs)
cfg, _, _ := config.Load()
```

### In `config.Init()`

```go
// After successful initialization
config.SetInitialVersion()
```

This ensures new installations have a baseline version for future migrations.

## Migration Rules

### DO
- ✅ Replace all container template files (Dockerfile, docker-compose.yml, etc.)
- ✅ Replace all script files (clipboard sync, network filter, etc.)
- ✅ Add new config fields from defaults
- ✅ Preserve all existing user config values
- ✅ Create backups before modifying user files

### DON'T
- ❌ Delete user config values
- ❌ Overwrite user customizations
- ❌ Modify files in `~/.config/construct-cli/home/` (user agent configs)
- ❌ Touch volumes or containers
- ❌ Require user input during migration

## Version Comparison

Uses simple semver comparison (X.Y.Z format):

```go
compareVersions("0.4.0", "0.3.0")  // returns 1 (newer)
compareVersions("0.3.0", "0.4.0")  // returns -1 (older)
compareVersions("0.3.0", "0.3.0")  // returns 0 (equal)
```

Supports versions with or without 'v' prefix: `0.4.0` or `v0.4.0`

## Future Migrations

To add migration logic for specific versions:

```go
func RunMigrations() error {
    installed := GetInstalledVersion()
    current := constants.Version

    // Always update templates and merge packages
    if err := updateContainerTemplates(); err != nil {
        return err
    }
    if err := mergePackagesFile(); err != nil {
        return err
    }

    // Version-specific migrations
    if compareVersions(installed, "0.5.0") < 0 && compareVersions(current, "0.5.0") >= 0 {
        // Specific migration for 0.5.0
        if err := migrateTo_0_5_0(); err != nil {
            return err
        }
    }

    // Update version last
    return SetInstalledVersion(current)
}
```

## Error Handling

- Migrations fail fast - any error stops the process
- Backups are created before modifications
- Clear error messages guide users to manual fixes if needed
- Failed migrations don't update `.version`, allowing retry on next run

## Files Modified by Migrations

**Always Replaced:**
- `~/.config/construct-cli/container/Dockerfile`
- `~/.config/construct-cli/container/docker-compose.yml`
- `~/.config/construct-cli/container/entrypoint.sh`
- `~/.config/construct-cli/container/update-all.sh`
- `~/.config/construct-cli/container/network-filter.sh`
- `~/.config/construct-cli/container/clipboard-bridge.sh`
- `~/.config/construct-cli/scripts/clipboard-sync-macos.sh`
- `~/.config/construct-cli/scripts/clipboard-sync-linux.sh`

**Merged (User Settings Preserved):**
- `~/.config/construct-cli/config.toml` (backup created)

**Never Modified:**
- `~/.config/construct-cli/home/*` (user agent configurations)
- Container volumes
- Running containers
