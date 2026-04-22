package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/ourstudio-se/devtui/internal/msgs"

	tea "charm.land/bubbletea/v2"
	"github.com/joho/godotenv"
)

// escapeRe matches any ANSI escape sequence or carriage return.
var escapeRe = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]|\x1b[\[\]()#][^\x1b]*|\x1b.|\r`)

// sgrRe matches only SGR sequences (colors, bold, reset — ending in 'm').
var sgrRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// sanitizeLog whitelists SGR color codes and strips all other escape
// sequences and carriage returns. This is safe inside a Bubble Tea
// viewport because SGR only toggles text style — it never moves the
// cursor or clears the screen.
func sanitizeLog(s string) string {
	// Extract all SGR sequences with their positions
	sgrs := sgrRe.FindAllStringIndex(s, -1)
	sgrSet := make(map[int]bool, len(sgrs))
	for _, loc := range sgrs {
		sgrSet[loc[0]] = true
	}

	return escapeRe.ReplaceAllStringFunc(s, func(match string) string {
		// Find where this match starts in the original string to
		// check if it's an SGR we want to keep. Since
		// ReplaceAllStringFunc doesn't give us the index, we
		// instead just re-test the match itself.
		if sgrRe.MatchString(match) {
			return match // keep colors
		}
		return "" // strip everything else
	})
}

// Manager handles process lifecycle for all services.
type Manager struct {
	projectRoot  string
	composeFile  string
	envVars      map[string]string
	processes    map[string]*managedProcess
	logFollowers map[string]context.CancelFunc
	mu           sync.Mutex
	program      *tea.Program
}

type managedProcess struct {
	cmd     *exec.Cmd // nil for re-adopted processes
	cancel  context.CancelFunc
	done    chan struct{}
	logFile *os.File // log file handle, nil for re-adopted
	pid     int      // for re-adopted (no cmd)
	pgid    int      // for re-adopted (no cmd)
}

func (p *managedProcess) getPID() int {
	if p.cmd != nil && p.cmd.Process != nil {
		return p.cmd.Process.Pid
	}
	return p.pid
}

func (p *managedProcess) getPGID() int {
	if p.cmd != nil && p.cmd.Process != nil {
		return p.cmd.Process.Pid // Setpgid makes pgid == pid
	}
	return p.pgid
}

func NewManager(projectRoot, composeFile string) *Manager {
	m := &Manager{
		projectRoot:  projectRoot,
		composeFile:  composeFile,
		envVars:      make(map[string]string),
		processes:    make(map[string]*managedProcess),
		logFollowers: make(map[string]context.CancelFunc),
	}

	envFile := filepath.Join(projectRoot, ".env")
	if envs, err := godotenv.Read(envFile); err == nil {
		m.envVars = envs
	}

	ensureDirs(projectRoot)

	return m
}

// SetProgram sets the tea.Program reference for sending messages.
func (m *Manager) SetProgram(p *tea.Program) {
	m.program = p
}

func (m *Manager) send(msg tea.Msg) {
	if m.program != nil {
		m.program.Send(msg)
	}
}

func (m *Manager) sendLog(name, line string) {
	cleaned := sanitizeLog(line)
	if strings.TrimSpace(cleaned) == "" {
		return
	}
	m.send(msgs.LogLine{ServiceName: name, Line: cleaned, Timestamp: time.Now()})
}

func (m *Manager) sendState(name string, state msgs.ServiceState, err error) {
	m.send(msgs.StateChanged{ServiceName: name, NewState: state, Error: err})
}

// runHookCmd runs a shell command via `sh -c`, streaming output to the
// service's log. Returns true on success. A non-zero exit code sets the
// service state to StateError and returns false. An empty cmdStr is a no-op
// and returns true.
func (m *Manager) runHookCmd(svc *Service, label, cmdStr string) bool {
	if strings.TrimSpace(cmdStr) == "" {
		return true
	}
	m.sendLog(svc.Name, fmt.Sprintf("Running %s hook: %s", label, cmdStr))

	cmd := exec.Command("sh", "-c", cmdStr)
	cmd.Dir = m.projectRoot
	cmd.Env = m.buildEnv()

	output, err := cmd.CombinedOutput()
	for _, line := range strings.Split(string(output), "\n") {
		m.sendLog(svc.Name, line)
	}
	if err != nil {
		m.sendLog(svc.Name, fmt.Sprintf("%s hook failed: %v", label, err))
		m.sendState(svc.Name, msgs.StateError, err)
		return false
	}
	return true
}

// composeFileArgs returns the -f flags for compose, including the override file if present.
func (m *Manager) composeFileArgs() []string {
	composeFile := filepath.Join(m.projectRoot, m.composeFile)
	args := []string{"-f", composeFile}
	// Docker Compose skips automatic override merging when -f is explicit,
	// so check for compose.override.yaml and include it.
	ext := filepath.Ext(m.composeFile)
	base := strings.TrimSuffix(m.composeFile, ext)
	override := filepath.Join(m.projectRoot, base+".override"+ext)
	if _, err := os.Stat(override); err == nil {
		args = append(args, "-f", override)
	}
	return args
}

// composeArgs returns the common docker compose prefix with forced ANSI color.
func (m *Manager) composeArgs() []string {
	args := []string{"compose", "--ansi", "always"}
	return append(args, m.composeFileArgs()...)
}

// composeArgsPlain returns the compose prefix without forced ANSI (for build).
func (m *Manager) composeArgsPlain() []string {
	args := []string{"compose"}
	return append(args, m.composeFileArgs()...)
}

// Toggle starts or stops a service.
func (m *Manager) Toggle(svc *Service) tea.Cmd {
	return func() tea.Msg {
		if svc.State == StateRunning || svc.State == StateStarting {
			m.stop(svc)
		} else {
			m.start(svc)
		}
		return nil
	}
}

// StartGroup starts all services in a range.
func (m *Manager) StartGroup(services []*Service, start, count int) tea.Cmd {
	return func() tea.Msg {
		for i := start; i < start+count && i < len(services); i++ {
			if services[i].State == StateStopped || services[i].State == StateError {
				m.start(services[i])
			}
		}
		return nil
	}
}

// StopGroup stops all services in a range.
func (m *Manager) StopGroup(services []*Service, start, count int) tea.Cmd {
	return func() tea.Msg {
		for i := start; i < start+count && i < len(services); i++ {
			if services[i].State == StateRunning || services[i].State == StateStarting {
				m.stop(services[i])
			}
		}
		return nil
	}
}

// StartAll starts all services.
func (m *Manager) StartAll(services []*Service) tea.Cmd {
	return func() tea.Msg {
		for _, svc := range services {
			if svc.State == StateStopped || svc.State == StateError {
				m.start(svc)
			}
		}
		return nil
	}
}

// StopAll stops all non-docker services and clears the state file.
func (m *Manager) StopAll(services []*Service) tea.Cmd {
	return func() tea.Msg {
		for _, svc := range services {
			if svc.Kind != KindDocker && (svc.State == StateRunning || svc.State == StateStarting) {
				m.stop(svc)
			}
		}
		// Clear state file so next TUI doesn't try to re-adopt dead processes
		SaveState(m.projectRoot, &StateFile{})
		return nil
	}
}

// StopDocker stops all docker services.
func (m *Manager) StopDocker(services []*Service) tea.Cmd {
	return func() tea.Msg {
		for _, svc := range services {
			if svc.Kind == KindDocker && (svc.State == StateRunning || svc.State == StateStarting) {
				m.stop(svc)
			}
		}
		return nil
	}
}

// Rebuild rebuilds the selected service (docker build / dotnet build / npm install).
func (m *Manager) Rebuild(svc *Service) tea.Cmd {
	return func() tea.Msg {
		wasRunning := svc.State == StateRunning || svc.State == StateStarting

		// Stop the service first if it's running
		if wasRunning {
			m.stop(svc)
			// Wait a moment for stop to complete
			time.Sleep(500 * time.Millisecond)
		}

		svc.LogBuffer.Clear()
		m.sendState(svc.Name, msgs.StateBuilding, nil)
		buildOk := false

		switch svc.Kind {
		case KindDocker:
			m.sendLog(svc.Name, "Rebuilding Docker image...")
			args := append(m.composeArgsPlain(), "build", "--progress", "plain", svc.ComposeService)
			cmd := exec.Command("docker", args...)
			cmd.Dir = m.projectRoot
			cmd.Env = m.buildEnv()
			output, err := cmd.CombinedOutput()
			for _, line := range strings.Split(string(output), "\n") {
				m.sendLog(svc.Name, line)
			}
			if err != nil {
				m.sendLog(svc.Name, fmt.Sprintf("Docker build failed: %v", err))
				m.sendState(svc.Name, msgs.StateError, err)
			} else {
				m.sendLog(svc.Name, "Docker build succeeded.")
				buildOk = true
			}

		case KindDotnet:
			projectPath := filepath.Join(m.projectRoot, svc.ProjectPath)
			m.sendLog(svc.Name, fmt.Sprintf("Building %s...", svc.Name))
			cmd := exec.Command("dotnet", "build", projectPath)
			cmd.Dir = m.projectRoot
			cmd.Env = m.buildEnv()
			output, err := cmd.CombinedOutput()
			for _, line := range strings.Split(string(output), "\n") {
				m.sendLog(svc.Name, line)
			}
			if err != nil {
				m.sendLog(svc.Name, fmt.Sprintf("Build failed: %v", err))
				m.sendState(svc.Name, msgs.StateError, err)
			} else {
				m.sendLog(svc.Name, "Build succeeded.")
				buildOk = true
			}

		case KindNPM:
			if svc.InstallCommand != "" {
				dir := filepath.Join(m.projectRoot, svc.Directory)
				m.sendLog(svc.Name, fmt.Sprintf("Running %s...", svc.InstallCommand))
				parts := strings.Fields(svc.InstallCommand)
				cmd := exec.Command(parts[0], parts[1:]...)
				cmd.Dir = dir
				cmd.Env = m.buildEnv()
				output, err := cmd.CombinedOutput()
				for _, line := range strings.Split(string(output), "\n") {
					m.sendLog(svc.Name, line)
				}
				if err != nil {
					m.sendLog(svc.Name, fmt.Sprintf("Install failed: %v", err))
					m.sendState(svc.Name, msgs.StateError, err)
				} else {
					m.sendLog(svc.Name, "Install succeeded.")
					buildOk = true
				}
			}
		}

		// Auto-restart after successful build
		if buildOk {
			m.start(svc)
		} else if !buildOk && svc.State != StateError {
			m.sendState(svc.Name, msgs.StateStopped, nil)
		}
		return nil
	}
}

// Build runs dotnet build on the solution.
func (m *Manager) Build() tea.Cmd {
	return func() tea.Msg {
		m.sendLog("[build]", "Building dotnet solution...")

		cmd := exec.Command("dotnet", "build")
		cmd.Dir = m.projectRoot
		cmd.Env = m.buildEnv()

		output, err := cmd.CombinedOutput()
		for _, line := range strings.Split(string(output), "\n") {
			m.sendLog("[build]", line)
		}

		if err != nil {
			m.sendLog("[build]", fmt.Sprintf("Build failed: %v", err))
			return msgs.BuildDone{Success: false}
		}
		m.sendLog("[build]", "Build succeeded.")
		return msgs.BuildDone{Success: true}
	}
}

// DetectRunning checks for already-running docker containers and port usage.
func (m *Manager) DetectRunning(services []*Service) tea.Cmd {
	return func() tea.Msg {
		m.adoptRunningServices(services)
		m.detectDockerState(services)
		m.detectPortState(services)
		return nil
	}
}

// PollDockerStatus returns a tick command that periodically checks container state.
func (m *Manager) PollDockerStatus(services []*Service) tea.Cmd {
	return tea.Tick(5*time.Second, func(t time.Time) tea.Msg {
		m.detectDockerState(services)
		m.detectResourceUsage(services)
		return msgs.TickMsg(t)
	})
}

func (m *Manager) start(svc *Service) {
	svc.LogBuffer.Clear()
	m.sendState(svc.Name, msgs.StateStarting, nil)

	switch svc.Kind {
	case KindDocker:
		go m.startDocker(svc)
	case KindDotnet:
		go m.startDotnet(svc)
	case KindNPM:
		go m.startNPM(svc)
	}
}

func (m *Manager) stop(svc *Service) {
	m.sendState(svc.Name, msgs.StateStopping, nil)

	switch svc.Kind {
	case KindDocker:
		go m.stopDocker(svc)
	case KindDotnet, KindNPM:
		go m.stopProcess(svc)
	}
}

// --- Docker ---

func (m *Manager) startDocker(svc *Service) {
	if !m.runHookCmd(svc, "pre-start", svc.PreStartCmd) {
		return
	}

	args := append(m.composeArgs(), "up", "-d", svc.ComposeService)
	cmd := exec.Command("docker", args...)
	cmd.Dir = m.projectRoot
	cmd.Env = m.buildEnv()

	output, err := cmd.CombinedOutput()
	for _, line := range strings.Split(string(output), "\n") {
		m.sendLog(svc.Name, line)
	}

	if err != nil {
		m.sendState(svc.Name, msgs.StateError, err)
		return
	}

	m.sendState(svc.Name, msgs.StateRunning, nil)

	// Run post-start hook in the background so log following can start in parallel.
	if strings.TrimSpace(svc.PostStartCmd) != "" {
		go m.runHookCmd(svc, "post-start", svc.PostStartCmd)
	}

	// Start log follower
	m.followDockerLogs(svc)
}

func (m *Manager) stopDocker(svc *Service) {
	m.mu.Lock()
	if cancel, ok := m.logFollowers[svc.Name]; ok {
		cancel()
		delete(m.logFollowers, svc.Name)
	}
	m.mu.Unlock()

	args := append(m.composeArgs(), "stop", svc.ComposeService)
	cmd := exec.Command("docker", args...)
	cmd.Dir = m.projectRoot
	cmd.Env = m.buildEnv()

	output, err := cmd.CombinedOutput()
	for _, line := range strings.Split(string(output), "\n") {
		m.sendLog(svc.Name, line)
	}

	if err != nil {
		m.sendState(svc.Name, msgs.StateError, err)
		return
	}

	m.sendState(svc.Name, msgs.StateStopped, nil)
}

func (m *Manager) followDockerLogs(svc *Service) {
	ctx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	m.logFollowers[svc.Name] = cancel
	m.mu.Unlock()

	env := m.buildEnv()

	// Bulk-load recent history directly into the buffer (no per-line messages,
	// so the UI doesn't visually scroll through them on startup).
	histArgs := append(m.composeArgs(), "logs", "--tail=200", svc.ComposeService)
	histCmd := exec.CommandContext(ctx, "docker", histArgs...)
	histCmd.Dir = m.projectRoot
	histCmd.Env = env
	if out, err := histCmd.Output(); err == nil {
		scanner := bufio.NewScanner(bytes.NewReader(out))
		for scanner.Scan() {
			cleaned := sanitizeLog(scanner.Text())
			if strings.TrimSpace(cleaned) != "" {
				svc.LogBuffer.Write(cleaned)
			}
		}
	}

	// Now follow only new output.
	args := append(m.composeArgs(), "logs", "-f", "--since=0s", svc.ComposeService)
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Dir = m.projectRoot
	cmd.Env = env

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return
	}
	cmd.Stderr = cmd.Stdout

	if err := cmd.Start(); err != nil {
		return
	}

	go func() {
		m.streamOutput(svc.Name, stdout)
		cmd.Wait()
	}()
}

// --- Dotnet ---

func (m *Manager) startDotnet(svc *Service) {
	if !m.runHookCmd(svc, "pre-start", svc.PreStartCmd) {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	projectPath := filepath.Join(m.projectRoot, svc.ProjectPath)

	cmd := exec.CommandContext(ctx, "dotnet", "run", "--no-build", "--project", projectPath)
	cmd.Dir = m.projectRoot
	cmd.Env = m.buildEnv()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// Redirect stdout/stderr to log file (survives TUI exit)
	logPath := logFilePath(m.projectRoot, svc.Name)
	lf, err := os.Create(logPath)
	if err != nil {
		m.sendState(svc.Name, msgs.StateError, err)
		cancel()
		return
	}
	cmd.Stdout = lf
	cmd.Stderr = lf

	if err := cmd.Start(); err != nil {
		m.sendState(svc.Name, msgs.StateError, err)
		lf.Close()
		cancel()
		return
	}

	done := make(chan struct{})
	m.mu.Lock()
	m.processes[svc.Name] = &managedProcess{cmd: cmd, cancel: cancel, done: done, logFile: lf}
	m.saveCurrentState()
	m.mu.Unlock()

	m.sendState(svc.Name, msgs.StateRunning, nil)

	if strings.TrimSpace(svc.PostStartCmd) != "" {
		go m.runHookCmd(svc, "post-start", svc.PostStartCmd)
	}

	// Tail the log file for display
	logCtx, logCancel := context.WithCancel(context.Background())
	m.mu.Lock()
	m.logFollowers[svc.Name] = logCancel
	m.mu.Unlock()
	go m.tailLogFile(logCtx, svc.Name, logPath)

	go func() {
		err := cmd.Wait()
		lf.Close()
		close(done)
		m.mu.Lock()
		delete(m.processes, svc.Name)
		m.removeServiceFromState(svc.Name)
		m.mu.Unlock()

		// Cancel log tailer
		m.mu.Lock()
		if cancel, ok := m.logFollowers[svc.Name]; ok {
			cancel()
			delete(m.logFollowers, svc.Name)
		}
		m.mu.Unlock()

		exitCode := 0
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			}
		}

		m.send(msgs.ProcessExited{ServiceName: svc.Name, ExitCode: exitCode, Error: err})
	}()
}

// --- NPM ---

func (m *Manager) startNPM(svc *Service) {
	if !m.runHookCmd(svc, "pre-start", svc.PreStartCmd) {
		return
	}

	dir := filepath.Join(m.projectRoot, svc.Directory)

	// Open log file early so install output goes there too
	logPath := logFilePath(m.projectRoot, svc.Name)
	lf, err := os.Create(logPath)
	if err != nil {
		m.sendState(svc.Name, msgs.StateError, err)
		return
	}

	// Start tailing the log file for display
	logCtx, logCancel := context.WithCancel(context.Background())
	m.mu.Lock()
	m.logFollowers[svc.Name] = logCancel
	m.mu.Unlock()
	go m.tailLogFile(logCtx, svc.Name, logPath)

	if svc.InstallCommand != "" {
		m.sendLog(svc.Name, fmt.Sprintf("Running %s...", svc.InstallCommand))
		parts := strings.Fields(svc.InstallCommand)
		installCmd := exec.Command(parts[0], parts[1:]...)
		installCmd.Dir = dir
		installCmd.Env = m.buildEnv()
		installCmd.Stdout = lf
		installCmd.Stderr = lf

		if err := installCmd.Run(); err != nil {
			lf.Close()
			logCancel()
			m.mu.Lock()
			delete(m.logFollowers, svc.Name)
			m.mu.Unlock()
			m.sendState(svc.Name, msgs.StateError, err)
			return
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	startParts := strings.Fields(svc.StartCommand)
	cmd := exec.CommandContext(ctx, startParts[0], startParts[1:]...)
	cmd.Dir = dir
	cmd.Env = m.buildEnv()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdout = lf
	cmd.Stderr = lf

	if err := cmd.Start(); err != nil {
		m.sendState(svc.Name, msgs.StateError, err)
		lf.Close()
		logCancel()
		m.mu.Lock()
		delete(m.logFollowers, svc.Name)
		m.mu.Unlock()
		cancel()
		return
	}

	done := make(chan struct{})
	m.mu.Lock()
	m.processes[svc.Name] = &managedProcess{cmd: cmd, cancel: cancel, done: done, logFile: lf}
	m.saveCurrentState()
	m.mu.Unlock()

	m.sendState(svc.Name, msgs.StateRunning, nil)

	if strings.TrimSpace(svc.PostStartCmd) != "" {
		go m.runHookCmd(svc, "post-start", svc.PostStartCmd)
	}

	go func() {
		err := cmd.Wait()
		lf.Close()
		close(done)
		m.mu.Lock()
		delete(m.processes, svc.Name)
		m.removeServiceFromState(svc.Name)
		m.mu.Unlock()

		// Cancel log tailer
		m.mu.Lock()
		if cancel, ok := m.logFollowers[svc.Name]; ok {
			cancel()
			delete(m.logFollowers, svc.Name)
		}
		m.mu.Unlock()

		exitCode := 0
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			}
		}
		m.send(msgs.ProcessExited{ServiceName: svc.Name, ExitCode: exitCode, Error: err})
	}()
}

// --- Process control ---

func (m *Manager) stopProcess(svc *Service) {
	m.mu.Lock()
	proc, ok := m.processes[svc.Name]
	m.mu.Unlock()

	if !ok {
		m.sendState(svc.Name, msgs.StateStopped, nil)
		return
	}

	pgid := proc.getPGID()
	if pgid > 0 {
		syscall.Kill(-pgid, syscall.SIGINT)
	}

	select {
	case <-proc.done:
	case <-time.After(5 * time.Second):
		if pgid > 0 {
			syscall.Kill(-pgid, syscall.SIGKILL)
		}
		<-proc.done
	}

	// Cancel log tailer
	m.mu.Lock()
	if cancel, ok := m.logFollowers[svc.Name]; ok {
		cancel()
		delete(m.logFollowers, svc.Name)
	}
	m.mu.Unlock()

	m.sendState(svc.Name, msgs.StateStopped, nil)
}

// --- Utility ---

func (m *Manager) streamOutput(serviceName string, reader io.Reader) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		m.sendLog(serviceName, scanner.Text())
	}
}

func (m *Manager) buildEnv() []string {
	env := os.Environ()
	for k, v := range m.envVars {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}
	// Force color output for piped processes
	env = append(env,
		"DOTNET_SYSTEM_CONSOLE_ALLOW_ANSI_COLOR_REDIRECTION=true",   // .NET
		"LOGGING__CONSOLE__FORMATTERNAME=simple",                    // .NET structured logging
		"LOGGING__CONSOLE__FORMATTEROPTIONS__COLORBEHAVIOR=Enabled", // .NET console colors
		"FORCE_COLOR=1",           // Node.js / npm / chalk
		"TERM=xterm-256color",     // general terminal capability
		"BUILDKIT_PROGRESS=plain", // Docker BuildKit: no interactive progress
	)
	return env
}

func (m *Manager) detectDockerState(services []*Service) {
	args := append(m.composeArgs(), "ps", "--format", "json")
	cmd := exec.Command("docker", args...)
	cmd.Dir = m.projectRoot
	cmd.Env = m.buildEnv()

	output, err := cmd.Output()
	if err != nil {
		return
	}

	type containerInfo struct {
		Service string `json:"Service"`
		State   string `json:"State"`
	}

	running := make(map[string]bool)
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var info containerInfo
		if err := json.Unmarshal([]byte(line), &info); err != nil {
			continue
		}
		if info.State == "running" {
			running[info.Service] = true
		}
	}

	for _, svc := range services {
		if svc.Kind != KindDocker {
			continue
		}
		if svc.State == StateStarting || svc.State == StateStopping {
			continue
		}
		if running[svc.ComposeService] {
			if svc.State != StateRunning {
				m.sendState(svc.Name, msgs.StateRunning, nil)
				m.mu.Lock()
				_, hasFollower := m.logFollowers[svc.Name]
				m.mu.Unlock()
				if !hasFollower {
					m.followDockerLogs(svc)
				}
			}
		} else {
			if svc.State == StateRunning {
				m.sendState(svc.Name, msgs.StateStopped, nil)
			}
		}
	}
}

func (m *Manager) detectResourceUsage(services []*Service) {
	stats := make(map[string]msgs.ResourceStats)

	// Docker services: get container IDs then docker stats
	composeToName := make(map[string]string)
	for _, svc := range services {
		if svc.Kind == KindDocker && svc.ComposeService != "" && svc.State == StateRunning {
			composeToName[svc.ComposeService] = svc.Name
		}
	}

	if len(composeToName) > 0 {
		// Map compose service -> container ID
		args := append(m.composeArgsPlain(), "ps", "--format", "json")
		cmd := exec.Command("docker", args...)
		cmd.Dir = m.projectRoot
		cmd.Env = m.buildEnv()
		output, err := cmd.Output()
		if err == nil {
			type psInfo struct {
				Service string `json:"Service"`
				ID      string `json:"ID"`
				State   string `json:"State"`
			}

			idToName := make(map[string]string) // container ID -> service name
			var containerIDs []string
			for _, line := range strings.Split(string(output), "\n") {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				var info psInfo
				if err := json.Unmarshal([]byte(line), &info); err != nil {
					continue
				}
				if info.State != "running" {
					continue
				}
				if svcName, ok := composeToName[info.Service]; ok {
					idToName[info.ID] = svcName
					containerIDs = append(containerIDs, info.ID)
				}
			}

			if len(containerIDs) > 0 {
				statsArgs := append([]string{"stats", "--no-stream", "--format", "{{json .}}"}, containerIDs...)
				statsCmd := exec.Command("docker", statsArgs...)
				statsOutput, err := statsCmd.Output()
				if err == nil {
					type dockerStatsJSON struct {
						ID       string `json:"ID"`
						CPUPerc  string `json:"CPUPerc"`
						MemUsage string `json:"MemUsage"`
					}
					for _, line := range strings.Split(string(statsOutput), "\n") {
						line = strings.TrimSpace(line)
						if line == "" {
							continue
						}
						var ds dockerStatsJSON
						if err := json.Unmarshal([]byte(line), &ds); err != nil {
							continue
						}
						svcName, ok := idToName[ds.ID]
						if !ok {
							continue
						}
						cpu := parsePercent(ds.CPUPerc)
						used, limit := parseMemUsage(ds.MemUsage)
						stats[svcName] = msgs.ResourceStats{
							CPUPercent: cpu,
							MemUsageMB: used,
							MemLimitMB: limit,
						}
					}
				}
			}
		}
	}

	// Process (dotnet/npm) services
	m.mu.Lock()
	procs := make(map[string]int)
	for name, proc := range m.processes {
		if pid := proc.getPID(); pid > 0 {
			procs[name] = pid
		}
	}
	m.mu.Unlock()

	for name, pid := range procs {
		cpu, rssKB := getProcessGroupStats(pid)
		stats[name] = msgs.ResourceStats{
			CPUPercent: cpu,
			MemUsageMB: float64(rssKB) / 1024.0,
		}
	}

	m.send(msgs.ResourceUpdate{Stats: stats})
}

func parsePercent(s string) float64 {
	s = strings.TrimSuffix(s, "%")
	s = strings.TrimSpace(s)
	var v float64
	fmt.Sscanf(s, "%f", &v)
	return v
}

func parseMemUsage(s string) (used, limit float64) {
	parts := strings.Split(s, "/")
	if len(parts) != 2 {
		return 0, 0
	}
	used = parseMemValue(strings.TrimSpace(parts[0]))
	limit = parseMemValue(strings.TrimSpace(parts[1]))
	return
}

func parseMemValue(s string) float64 {
	s = strings.TrimSpace(s)
	i := 0
	for i < len(s) && (s[i] == '.' || (s[i] >= '0' && s[i] <= '9')) {
		i++
	}
	if i == 0 {
		return 0
	}
	var val float64
	fmt.Sscanf(s[:i], "%f", &val)
	unit := strings.ToLower(s[i:])

	switch unit {
	case "b":
		return val / (1024 * 1024)
	case "kib", "kb":
		return val / 1024
	case "mib", "mb":
		return val
	case "gib", "gb":
		return val * 1024
	case "tib", "tb":
		return val * 1024 * 1024
	}
	return val
}

func getProcessGroupStats(pgid int) (cpuPercent float64, rssKB int64) {
	pgrepOut, err := exec.Command("pgrep", "-g", fmt.Sprintf("%d", pgid)).Output()
	if err != nil {
		return 0, 0
	}

	var pids []string
	for _, line := range strings.Split(strings.TrimSpace(string(pgrepOut)), "\n") {
		if pid := strings.TrimSpace(line); pid != "" {
			pids = append(pids, pid)
		}
	}
	if len(pids) == 0 {
		return 0, 0
	}

	psOut, err := exec.Command("ps", "-o", "pcpu=,rss=", "-p", strings.Join(pids, ",")).Output()
	if err != nil {
		return 0, 0
	}

	for _, line := range strings.Split(string(psOut), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) >= 2 {
			var cpu float64
			var rss int64
			fmt.Sscanf(fields[0], "%f", &cpu)
			fmt.Sscanf(fields[1], "%d", &rss)
			cpuPercent += cpu
			rssKB += rss
		}
	}
	return
}

func (m *Manager) detectPortState(services []*Service) {
	for _, svc := range services {
		if svc.Kind == KindDocker || svc.Port == 0 {
			continue
		}
		if svc.State != StateStopped {
			continue
		}
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("localhost:%d", svc.Port), 500*time.Millisecond)
		if err != nil {
			continue
		}
		conn.Close()

		// Port is open but service wasn't adopted — it's external
		pid := findPIDByPort(svc.Port)
		m.sendLog(svc.Name, fmt.Sprintf("Port %d is in use (externally managed, PID %d)", svc.Port, pid))
		m.send(msgs.ExternalServiceDetected{ServiceName: svc.Name, PID: pid})
	}
}

// getProcessGroupID returns the PGID for a given PID.
// Works on both Linux and macOS (ps -o pgid= is POSIX).
func getProcessGroupID(pid int) int {
	out, err := exec.Command("ps", "-o", "pgid=", "-p", fmt.Sprintf("%d", pid)).Output()
	if err != nil {
		return 0
	}
	pgid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0
	}
	return pgid
}

// findPIDByPort uses lsof to find the PID of the process listening on a port.
// Works on both Linux and macOS.
func findPIDByPort(port int) int {
	out, err := exec.Command("lsof", "-ti", fmt.Sprintf(":%d", port)).Output()
	if err != nil {
		return 0
	}
	// lsof may return multiple PIDs; take the first
	line := strings.TrimSpace(strings.Split(string(out), "\n")[0])
	pid, err := strconv.Atoi(line)
	if err != nil {
		return 0
	}
	return pid
}

// adoptRunningServices re-adopts services from a previous TUI session using the state file.
func (m *Manager) adoptRunningServices(services []*Service) {
	sf, _ := LoadState(m.projectRoot)
	if len(sf.Services) == 0 {
		return
	}

	// Build lookup
	svcMap := make(map[string]*Service)
	for _, svc := range services {
		svcMap[svc.Name] = svc
	}

	changed := false
	for _, entry := range sf.Services {
		svc, ok := svcMap[entry.Name]
		if !ok {
			changed = true
			continue
		}

		// Check if PID is still alive
		if err := syscall.Kill(entry.PID, 0); err != nil {
			// Process is dead — remove stale entry
			changed = true
			continue
		}

		// Verify the port matches (guards against PID recycling)
		if svc.Port > 0 {
			conn, err := net.DialTimeout("tcp", fmt.Sprintf("localhost:%d", svc.Port), 500*time.Millisecond)
			if err != nil {
				changed = true
				continue
			}
			conn.Close()

			// Verify the port process belongs to our process group.
			// dotnet run spawns a child that binds the port, so portPID
			// won't match entry.PID — but they share the same PGID.
			portPID := findPIDByPort(svc.Port)
			if portPID > 0 && portPID != entry.PID {
				portPGID := getProcessGroupID(portPID)
				if portPGID == 0 || portPGID != entry.PGID {
					// Different process group — PID was recycled
					changed = true
					continue
				}
			}
		}

		// Re-adopt this service — set state directly so detectPortState
		// (which runs next) sees it's already running and skips it.
		svc.State = StateRunning
		m.sendLog(svc.Name, fmt.Sprintf("Re-adopting running service (PID %d)", entry.PID))

		done := make(chan struct{})
		m.mu.Lock()
		m.processes[svc.Name] = &managedProcess{
			pid:  entry.PID,
			pgid: entry.PGID,
			done: done,
		}
		m.mu.Unlock()

		m.sendState(svc.Name, msgs.StateRunning, nil)

		// Bulk-load existing log file
		logPath := logFilePath(m.projectRoot, svc.Name)
		m.bulkLoadLogFile(svc, logPath)

		// Start tailing
		logCtx, logCancel := context.WithCancel(context.Background())
		m.mu.Lock()
		m.logFollowers[svc.Name] = logCancel
		m.mu.Unlock()
		go m.tailLogFile(logCtx, svc.Name, logPath)

		// Monitor PID for exit
		go m.monitorPID(svc, entry.PID, done)
	}

	if changed {
		// Re-save state with stale entries removed
		m.mu.Lock()
		m.saveCurrentState()
		m.mu.Unlock()
	}
}

// monitorPID polls to detect when a re-adopted process exits.
func (m *Manager) monitorPID(svc *Service, pid int, done chan struct{}) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			if err := syscall.Kill(pid, 0); err != nil {
				close(done)
				m.mu.Lock()
				delete(m.processes, svc.Name)
				m.mu.Unlock()
				m.removeServiceFromState(svc.Name)

				// Cancel log tailer
				m.mu.Lock()
				if cancel, ok := m.logFollowers[svc.Name]; ok {
					cancel()
					delete(m.logFollowers, svc.Name)
				}
				m.mu.Unlock()

				m.send(msgs.ProcessExited{ServiceName: svc.Name, ExitCode: -1, Error: nil})
				return
			}
		}
	}
}

// StopExternalProcess stops a process that was not started by this TUI.
func (m *Manager) StopExternalProcess(svc *Service) tea.Cmd {
	return func() tea.Msg {
		if svc.ExternalPID <= 0 {
			m.sendState(svc.Name, msgs.StateStopped, nil)
			return nil
		}

		m.sendState(svc.Name, msgs.StateStopping, nil)
		m.sendLog(svc.Name, fmt.Sprintf("Stopping external process (PID %d)...", svc.ExternalPID))

		syscall.Kill(svc.ExternalPID, syscall.SIGINT)

		deadline := time.After(5 * time.Second)
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-deadline:
				syscall.Kill(svc.ExternalPID, syscall.SIGKILL)
				time.Sleep(500 * time.Millisecond)
				svc.ExternalPID = 0
				m.sendState(svc.Name, msgs.StateStopped, nil)
				return nil
			case <-ticker.C:
				if err := syscall.Kill(svc.ExternalPID, 0); err != nil {
					svc.ExternalPID = 0
					m.sendState(svc.Name, msgs.StateStopped, nil)
					return nil
				}
			}
		}
	}
}

// Detach cleans up log followers without stopping processes.
// The state file is preserved so the next TUI can re-adopt.
func (m *Manager) Detach() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for name, cancel := range m.logFollowers {
		cancel()
		delete(m.logFollowers, name)
	}
}
