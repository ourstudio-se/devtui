package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/ourstudio-se/devtui/internal/config"
	"github.com/ourstudio-se/devtui/internal/ui"

	tea "charm.land/bubbletea/v2"
)

// configCandidates lists the filenames we look for, in preference order.
var configCandidates = []string{"devtui.yaml", ".devtui.yaml"}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if err := config.Discover(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	model := ui.NewModel(cfg)

	p := tea.NewProgram(model)
	model.SetProgram(p)
	mgr := model.Manager()

	// Children are started with Setpgid:true; if the parent dies first they
	// are reparented to PID 1 and survive as orphans. Bubbletea consumes
	// ctrl+c as a key event in raw mode, so SIGINT here only fires when the
	// signal comes from outside (kill, terminal close, shell exit).
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGINT)
	go func() {
		<-sigCh
		mgr.StopAllProcesses()
		p.Send(tea.Quit())
	}()

	// Belt-and-suspenders for any unexpected exit path (panic recovery etc.).
	defer mgr.StopAllProcesses()

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// loadConfig walks up from the current working directory looking for
// devtui.yaml / .devtui.yaml. If found, the file is loaded and its directory
// is used as the default project root. If not found, an in-memory default
// Config is returned with ProjectRoot set to the git repo root (or CWD).
func loadConfig() (*config.Config, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("getting working directory: %w", err)
	}

	if path := findConfigFile(cwd); path != "" {
		return config.Load(path)
	}

	root := gitRoot(cwd)
	if root == "" {
		root = cwd
	}
	return &config.Config{ProjectRoot: root}, nil
}

// findConfigFile walks up from startDir toward the filesystem root, stopping
// at a git root if one is encountered. Returns the absolute path of the first
// matching config file, or "".
func findConfigFile(startDir string) string {
	dir := startDir
	for {
		for _, name := range configCandidates {
			candidate := filepath.Join(dir, name)
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				abs, _ := filepath.Abs(candidate)
				return abs
			}
		}

		// Stop at a git root (don't escape the repo looking for config)
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return ""
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// gitRoot returns the git repository root containing startDir, or "" if none.
func gitRoot(startDir string) string {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = startDir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
