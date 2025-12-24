package sys

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/EstebanForge/construct-cli/internal/config"
	"github.com/EstebanForge/construct-cli/internal/ui"
)

// SSHImport scans the host's ~/.ssh directory and allows the user to import keys.
func SSHImport() {
	home, err := os.UserHomeDir()
	if err != nil {
		ui.GumError(fmt.Sprintf("Failed to determine user home directory: %v", err))
		return
	}

	hostSSHDir := filepath.Join(home, ".ssh")
	targetSSHDir := filepath.Join(config.GetConfigDir(), "home", ".ssh")

	// 1. Scan for keys FIRST
	if _, err := os.Stat(hostSSHDir); os.IsNotExist(err) {
		ui.GumInfo(fmt.Sprintf("SSH directory not found at %s. Nothing to import.", hostSSHDir))
		return
	}

	entries, err := os.ReadDir(hostSSHDir)
	if err != nil {
		ui.GumError(fmt.Sprintf("Failed to read host SSH directory (%s): %v", hostSSHDir, err))
		return
	}

	var candidates []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()

		// Skip public keys, known_hosts, config, etc.
		if strings.HasSuffix(name, ".pub") || name == "known_hosts" || name == "config" || name == "authorized_keys" {
			continue
		}

		// Look for common private key patterns
		if strings.HasPrefix(name, "id_") || strings.HasSuffix(name, ".pem") {
			candidates = append(candidates, name)
		}
	}

	if len(candidates) == 0 {
		ui.GumInfo("No private SSH keys found in ~/.ssh to import.")
		return
	}

	// 2. Inform and Confirm (only if keys exist)
	fmt.Println()
	if ui.GumAvailable() {
		styleCmd := ui.GetGumCommand("style",
			"--foreground", "212", "--border", "rounded", "--padding", "1 2", "--width", "60",
			"SSH Key Import",
			"",
			fmt.Sprintf("Found %d key(s) in your host's ~/.ssh folder.", len(candidates)),
			"This tool allows you to securely copy specific keys into The Construct.",
			"",
			"Imported keys are stored in the persistent config volume",
			"and will be available in /home/construct/.ssh/ inside.",
		)
		styleCmd.Stdout = os.Stdout
		if err := styleCmd.Run(); err != nil {
			ui.LogDebug("failed to render info: %v", err)
		}
		fmt.Println()

		if !ui.GumConfirm("Do you want to proceed with selecting keys to import?") {
			fmt.Println("Import canceled.")
			return
		}
	} else {
		fmt.Println("=== SSH Key Import ===")
		fmt.Printf("Found %d keys in ~/.ssh.\n", len(candidates))
		fmt.Print("\nDo you want to proceed with selecting keys to import? [y/N]: ")
		var resp string
		if _, err := fmt.Scanln(&resp); err != nil || (strings.ToLower(resp) != "y" && strings.ToLower(resp) != "yes") {
			fmt.Println("Import canceled.")
			return
		}
	}

	// 3. Select keys
	var selected []string
	if ui.GumAvailable() {
		// Use Gum for selection
		args := []string{"choose", "--no-limit", "--header", "Select keys to import (Space to select, Enter to confirm):"}
		args = append(args, candidates...)
		cmd := ui.GetGumCommand(args...)

		// CRITICAL: Connect Stdin and Stderr to the terminal for UI rendering
		// Output() will capture Stdout (the result)
		cmd.Stdin = os.Stdin
		cmd.Stderr = os.Stderr

		output, err := cmd.Output()
		if err != nil {
			// User likely canceled (e.g. Ctrl+C or Esc)
			fmt.Println("Selection canceled.")
			return
		}
		selectionStr := strings.TrimSpace(string(output))
		if selectionStr != "" {
			selected = strings.Split(selectionStr, "\n")
		}
	} else {
		// Fallback simple selection (minimal)
		fmt.Println("\nAvailable keys in ~/.ssh:")
		for i, c := range candidates {
			fmt.Printf("[%d] %s\n", i+1, c)
		}
		fmt.Print("Enter numbers to import (comma separated, e.g. 1,3) or 'all': ")
		var input string
		if _, err := fmt.Scanln(&input); err != nil {
			fmt.Println("Selection canceled.")
			return
		}
		if input == "all" {
			selected = candidates
		} else {
			// Basic numeric parsing for fallback
			parts := strings.Split(input, ",")
			for _, p := range parts {
				idx := 0
				if _, err := fmt.Sscanf(strings.TrimSpace(p), "%d", &idx); err != nil {
					continue
				}
				if idx > 0 && idx <= len(candidates) {
					selected = append(selected, candidates[idx-1])
				}
			}
		}
	}

	if len(selected) == 0 {
		ui.GumInfo("No keys selected. Aborting.")
		return
	}

	// 4. Final Confirmation
	confirmMsg := fmt.Sprintf("Import %d key(s) into Construct?", len(selected))
	if ui.GumAvailable() {
		if !ui.GumConfirm(confirmMsg) {
			fmt.Println("Import canceled.")
			return
		}
	} else {
		fmt.Printf("%s [y/N]: ", confirmMsg)
		var resp string
		if _, err := fmt.Scanln(&resp); err != nil {
			resp = "n"
		}
		if strings.ToLower(resp) != "y" {
			fmt.Println("Import canceled.")
			return
		}
	}

	// 5. Execute Import
	if err := os.MkdirAll(targetSSHDir, 0700); err != nil {
		ui.GumError(fmt.Sprintf("Failed to create target SSH directory: %v", err))
		return
	}

	successCount := 0
	for _, key := range selected {
		src := filepath.Join(hostSSHDir, key)
		dst := filepath.Join(targetSSHDir, key)

		data, err := os.ReadFile(src)
		if err != nil {
			ui.LogWarning("Failed to read key %s: %v", key, err)
			continue
		}

		if err := os.WriteFile(dst, data, 0600); err != nil {
			ui.LogWarning("Failed to write key %s: %v", key, err)
			continue
		}

		// Also try to copy the matching .pub file if it exists (good practice)
		pubSrc := src + ".pub"
		if _, err := os.Stat(pubSrc); err == nil {
			pubData, err := os.ReadFile(pubSrc)
			if err == nil {
				if err := os.WriteFile(dst+".pub", pubData, 0644); err != nil {
					ui.LogWarning("Failed to write public key %s: %v", key, err)
				}
			}
		}
		successCount++
		if ui.GumAvailable() {
			fmt.Printf("%s  ✓ Imported: %s%s\n", ui.ColorGrey, key, ui.ColorReset)
		} else {
			fmt.Printf("  ✓ Imported: %s\n", key)
		}
	}

	// Optionally copy known_hosts
	knownHostsSrc := filepath.Join(hostSSHDir, "known_hosts")
	if _, err := os.Stat(knownHostsSrc); err == nil {
		if ui.GumConfirm("Copy 'known_hosts' to avoid fingerprint prompts?") {
			data, err := os.ReadFile(knownHostsSrc)
			if err == nil {
				if err := os.WriteFile(filepath.Join(targetSSHDir, "known_hosts"), data, 0600); err != nil {
					ui.LogWarning("Failed to write known_hosts: %v", err)
				} else {
					if ui.GumAvailable() {
						fmt.Printf("%s  ✓ Imported: known_hosts%s\n", ui.ColorGrey, ui.ColorReset)
					} else {
						fmt.Println("  ✓ Imported: known_hosts")
					}
				}
			}
		}
	}

	if successCount > 0 {
		ui.GumSuccess(fmt.Sprintf("Successfully imported %d key(s)!", successCount))
	} else {
		ui.GumError("Failed to import any keys.")
	}
}
