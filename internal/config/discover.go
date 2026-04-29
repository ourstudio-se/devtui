package config

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Discover runs auto-discovery for the categories enabled in cfg.Discover,
// then overlays the user-defined cfg.Groups on top of the discovered groups.
// The merged result replaces cfg.Groups. It also fills in cfg.ComposeFile if
// empty by probing the usual default names in cfg.ProjectRoot.
func Discover(cfg *Config) error {
	if cfg.ProjectRoot == "" {
		return fmt.Errorf("project root is not set")
	}

	// Auto-detect compose file if unset
	if cfg.ComposeFile == "" {
		cfg.ComposeFile = findComposeFile(cfg.ProjectRoot)
	}

	userGroups := cfg.Groups
	discovered, err := runDiscovery(cfg)
	if err != nil {
		return err
	}

	merged := mergeGroups(discovered, userGroups)

	totalServices := 0
	for _, g := range merged {
		totalServices += len(g.Services)
	}
	if totalServices == 0 {
		return fmt.Errorf("no services discovered and no services defined in config")
	}

	cfg.Groups = merged
	return nil
}

// composeFileCandidates lists the compose filenames probed in order.
var composeFileCandidates = []string{
	"compose.yaml",
	"compose.yml",
	"docker-compose.yaml",
	"docker-compose.yml",
}

func findComposeFile(root string) string {
	for _, name := range composeFileCandidates {
		if _, err := os.Stat(filepath.Join(root, name)); err == nil {
			return name
		}
	}
	return ""
}

func runDiscovery(cfg *Config) ([]Group, error) {
	var groups []Group

	if enabledPtr(cfg.Discover.Docker, true) && cfg.ComposeFile != "" {
		g, err := discoverDocker(cfg.ProjectRoot, cfg.ComposeFile)
		if err == nil && len(g.Services) > 0 {
			groups = append(groups, g)
		}
	}

	if enabledPtr(cfg.Discover.Dotnet.Enabled, true) {
		apiGroup, workerGroup, err := discoverDotnet(cfg.ProjectRoot, cfg.Discover.Dotnet)
		if err == nil {
			if len(apiGroup.Services) > 0 {
				groups = append(groups, apiGroup)
			}
			if len(workerGroup.Services) > 0 {
				groups = append(groups, workerGroup)
			}
		}
	}

	if enabledPtr(cfg.Discover.NPM.Enabled, true) {
		g, err := discoverNPM(cfg.ProjectRoot, cfg.Discover.NPM)
		if err == nil && len(g.Services) > 0 {
			groups = append(groups, g)
		}
	}

	return groups, nil
}

func enabledPtr(p *bool, def bool) bool {
	if p == nil {
		return def
	}
	return *p
}

// --- Docker discovery ---

func discoverDocker(root, composeFile string) (Group, error) {
	path := filepath.Join(root, composeFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return Group{}, err
	}

	var parsed struct {
		Services map[string]any `yaml:"services"`
	}
	if err := yaml.Unmarshal(data, &parsed); err != nil {
		return Group{}, err
	}

	names := make([]string, 0, len(parsed.Services))
	for name := range parsed.Services {
		names = append(names, name)
	}
	sort.Strings(names)

	g := Group{Name: "Dependencies", Kind: "docker"}
	for _, name := range names {
		g.Services = append(g.Services, ServiceConf{
			Name:           name,
			ComposeService: name,
		})
	}
	return g, nil
}

// --- Dotnet discovery ---

// appUrlPortRe extracts a TCP port from an applicationUrl value such as
// "http://localhost:5170" or "https://+:5001;http://+:5000".
var appUrlPortRe = regexp.MustCompile(`:(\d+)`)

// dotnetSkipSegments are path components that cause the walker to skip files.
var dotnetSkipSegments = []string{
	string(os.PathSeparator) + ".claude" + string(os.PathSeparator),
	string(os.PathSeparator) + "node_modules" + string(os.PathSeparator),
	string(os.PathSeparator) + "bin" + string(os.PathSeparator),
	string(os.PathSeparator) + "obj" + string(os.PathSeparator),
}

func discoverDotnet(root string, opts DotnetDiscoverConfig) (apis, workers Group, err error) {
	apis = Group{Name: "APIs", Kind: "dotnet"}
	workers = Group{Name: "Workers", Kind: "dotnet"}

	glob := opts.Glob
	if glob == "" {
		glob = "*.csproj"
	}

	exclude := opts.Exclude

	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			// Skip unreadable directories rather than aborting the walk.
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if name == ".claude" || name == "node_modules" || name == "bin" || name == "obj" || name == ".git" {
				return fs.SkipDir
			}
			return nil
		}

		if !matchGlob(glob, d.Name()) {
			return nil
		}
		// Belt-and-braces path filter (in case one of the skip dirs was reached via symlink etc.)
		for _, seg := range dotnetSkipSegments {
			if strings.Contains(path, seg) {
				return nil
			}
		}
		for _, ex := range exclude {
			if strings.Contains(path, ex) {
				return nil
			}
		}

		relPath, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}

		// Require sibling Properties/launchSettings.json — the IDE/CLI's
		// explicit "how to run this" manifest. Libraries and test projects
		// don't have it.
		launchPath := filepath.Join(filepath.Dir(path), "Properties", "launchSettings.json")
		if _, statErr := os.Stat(launchPath); statErr != nil {
			return nil
		}

		contents, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		text := string(contents)

		if strings.Contains(text, "<IsTestProject>true</IsTestProject>") {
			return nil
		}
		if strings.Contains(text, "<OutputType>Library</OutputType>") {
			return nil
		}

		sdk := extractSDK(text)
		kind := ""
		switch sdk {
		case "Microsoft.NET.Sdk.Web":
			kind = "api"
		case "Microsoft.NET.Sdk", "Microsoft.NET.Sdk.Worker":
			kind = "worker"
		default:
			// Unknown SDK — skip rather than guess.
			return nil
		}

		name := strings.TrimSuffix(d.Name(), filepath.Ext(d.Name()))
		if opts.StripPrefix != "" && strings.HasPrefix(name, opts.StripPrefix) {
			name = strings.TrimPrefix(name, opts.StripPrefix)
		}

		svc := ServiceConf{
			Name:    name,
			Project: relPath,
		}

		if kind == "api" {
			svc.Port = readLaunchSettingsPort(launchPath)
			apis.Services = append(apis.Services, svc)
		} else {
			workers.Services = append(workers.Services, svc)
		}

		return nil
	})

	if err != nil {
		return apis, workers, err
	}

	sort.SliceStable(apis.Services, func(i, j int) bool {
		pi, pj := apis.Services[i].Port, apis.Services[j].Port
		if pi == pj {
			return apis.Services[i].Name < apis.Services[j].Name
		}
		if pi == 0 {
			return false
		}
		if pj == 0 {
			return true
		}
		return pi < pj
	})
	sort.SliceStable(workers.Services, func(i, j int) bool {
		return workers.Services[i].Name < workers.Services[j].Name
	})

	return apis, workers, nil
}

// sdkRe captures Sdk attribute values like Sdk="Microsoft.NET.Sdk.Web".
var sdkRe = regexp.MustCompile(`<Project\b[^>]*\bSdk="([^"]+)"`)

func extractSDK(csproj string) string {
	m := sdkRe.FindStringSubmatch(csproj)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}

// readLaunchSettingsPort returns the first port found in applicationUrl, or
// if none, in the ASPNETCORE_URLS env var of any profile. Returns 0 if not
// parseable.
func readLaunchSettingsPort(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	// Strip UTF-8 BOM — Visual Studio writes launchSettings.json with one,
	// and encoding/json doesn't strip it.
	data = stripBOM(data)
	var ls struct {
		Profiles map[string]struct {
			ApplicationURL       string            `json:"applicationUrl"`
			EnvironmentVariables map[string]string `json:"environmentVariables"`
		} `json:"profiles"`
	}
	if err := json.Unmarshal(data, &ls); err != nil {
		return 0
	}

	// Prefer applicationUrl, first HTTP-ish port we find.
	for _, prof := range ls.Profiles {
		if p := firstPort(prof.ApplicationURL); p > 0 {
			return p
		}
	}
	for _, prof := range ls.Profiles {
		if urls, ok := prof.EnvironmentVariables["ASPNETCORE_URLS"]; ok {
			if p := firstPort(urls); p > 0 {
				return p
			}
		}
	}
	return 0
}

func firstPort(s string) int {
	matches := appUrlPortRe.FindAllStringSubmatch(s, -1)
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		var p int
		fmt.Sscanf(m[1], "%d", &p)
		if p > 0 && p < 65536 {
			return p
		}
	}
	return 0
}

// --- NPM discovery ---

func discoverNPM(root string, opts NPMDiscoverConfig) (Group, error) {
	scripts := opts.Scripts
	if len(scripts) == 0 {
		scripts = []string{"dev", "watch", "start"}
	}

	g := Group{Name: "Frontend", Kind: "npm"}

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if name == ".claude" || name == "node_modules" || name == "bin" || name == "obj" || name == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		if d.Name() != "package.json" {
			return nil
		}

		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		data = stripBOM(data)

		var pkg struct {
			Name    string            `json:"name"`
			Scripts map[string]string `json:"scripts"`
		}
		if err := json.Unmarshal(data, &pkg); err != nil {
			return nil
		}

		// Pick the first matching script name.
		var chosen string
		for _, s := range scripts {
			if _, ok := pkg.Scripts[s]; ok {
				chosen = s
				break
			}
		}
		if chosen == "" {
			return nil
		}

		dir := filepath.Dir(path)
		relDir, relErr := filepath.Rel(root, dir)
		if relErr != nil {
			return nil
		}

		name := pkg.Name
		if name == "" {
			name = filepath.Base(dir)
		}

		svc := ServiceConf{
			Name:         name,
			Directory:    relDir,
			StartCommand: "npm run " + chosen,
		}
		if _, err := os.Stat(filepath.Join(dir, "package-lock.json")); err == nil {
			svc.InstallCommand = "npm ci"
		}

		g.Services = append(g.Services, svc)
		return nil
	})

	if err != nil {
		return g, err
	}

	sort.SliceStable(g.Services, func(i, j int) bool {
		return g.Services[i].Name < g.Services[j].Name
	})
	return g, nil
}

// --- Overlay / merge ---

// mergeGroups overlays user-defined groups on top of discovered groups.
// Groups are matched by name. Within a matched group, services are matched by
// kind-specific key (compose_service / project / directory) with fallback to
// name. Explicit user fields override discovered fields. Unknown user groups
// and services append.
func mergeGroups(discovered, user []Group) []Group {
	// Index discovered groups by name for quick lookup. Preserve order via slice.
	result := make([]Group, len(discovered))
	copy(result, discovered)
	nameIdx := make(map[string]int, len(result))
	for i, g := range result {
		nameIdx[g.Name] = i
	}

	for _, ug := range user {
		if idx, ok := nameIdx[ug.Name]; ok {
			result[idx] = mergeServices(result[idx], ug)
		} else {
			result = append(result, ug)
			nameIdx[ug.Name] = len(result) - 1
		}
	}

	return result
}

func mergeServices(base, overlay Group) Group {
	// Merge group kind (overlay wins if set).
	if overlay.Kind != "" {
		base.Kind = overlay.Kind
	}

	// Build key func based on group kind.
	keyFn := serviceKey(base.Kind)

	// Index existing services.
	byKey := make(map[string]int, len(base.Services))
	for i, s := range base.Services {
		byKey[keyFn(s)] = i
	}

	for _, us := range overlay.Services {
		k := keyFn(us)
		if idx, ok := byKey[k]; ok {
			base.Services[idx] = mergeService(base.Services[idx], us)
		} else {
			base.Services = append(base.Services, us)
			byKey[k] = len(base.Services) - 1
		}
	}

	return base
}

func serviceKey(kind string) func(ServiceConf) string {
	return func(s ServiceConf) string {
		switch kind {
		case "docker":
			if s.ComposeService != "" {
				return "compose:" + s.ComposeService
			}
		case "dotnet":
			if s.Project != "" {
				return "project:" + s.Project
			}
		case "npm":
			if s.Directory != "" {
				return "dir:" + s.Directory
			}
		}
		return "name:" + s.Name
	}
}

// mergeService returns base with any explicit (non-zero) fields from overlay applied.
func mergeService(base, overlay ServiceConf) ServiceConf {
	if overlay.Name != "" {
		base.Name = overlay.Name
	}
	if overlay.ComposeService != "" {
		base.ComposeService = overlay.ComposeService
	}
	if overlay.Port != 0 {
		base.Port = overlay.Port
	}
	if overlay.Project != "" {
		base.Project = overlay.Project
	}
	if overlay.Directory != "" {
		base.Directory = overlay.Directory
	}
	if overlay.InstallCommand != "" {
		base.InstallCommand = overlay.InstallCommand
	}
	if overlay.StartCommand != "" {
		base.StartCommand = overlay.StartCommand
	}
	if len(overlay.DependsOn) > 0 {
		base.DependsOn = overlay.DependsOn
	}
	if overlay.PreStartCmd != "" {
		base.PreStartCmd = overlay.PreStartCmd
	}
	if overlay.PostStartCmd != "" {
		base.PostStartCmd = overlay.PostStartCmd
	}
	return base
}

// stripBOM removes a leading UTF-8 byte-order mark, if present.
func stripBOM(b []byte) []byte {
	if len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF {
		return b[3:]
	}
	return b
}

// matchGlob is a wrapper around filepath.Match that returns false instead of
// propagating the (rare) malformed-pattern error.
func matchGlob(pattern, name string) bool {
	ok, err := filepath.Match(pattern, name)
	if err != nil {
		return false
	}
	return ok
}
