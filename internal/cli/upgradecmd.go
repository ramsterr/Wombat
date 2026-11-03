package cli

import (
	"fmt"
	"os"
	"os/exec"
)

// cmdUpgrade is the in-app spelling of the install command. It has no release
// channel and no alternate method: every upgrade rebuilds current main and
// installs the same UI assets as the curl command in the README.
func cmdUpgrade(args []string) error {
	checkOnly := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--check", "-c":
			checkOnly = true
		case "--help", "-h":
			fmt.Println("usage: wombat upgrade [--check]")
			return nil
		case "--method", "-m", "--version", "-v":
			return fmt.Errorf("tagged-release upgrades are not supported; `wombat upgrade` rebuilds main")
		default:
			return fmt.Errorf("unknown argument %q", args[i])
		}
	}

	fmt.Println(dim("current: " + VersionString()))
	if checkOnly {
		fmt.Println("run `wombat upgrade` to rebuild the latest main")
		return nil
	}
	if _, err := exec.LookPath("sh"); err != nil {
		return fmt.Errorf("sh is required; on Windows run this inside WSL2 (Ubuntu):\n  %s", uiInstallCommand)
	}
	fmt.Println(dim("running: " + uiInstallCommand))
	cmd := exec.Command("sh", "-c", uiInstallCommand)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("update failed: %w", err)
	}
	return nil
}
