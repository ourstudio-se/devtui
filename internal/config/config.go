package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Config struct {
	ProjectRoot string         `yaml:"project_root"`
	ComposeFile string         `yaml:"compose_file"`
	EnvFile     string         `yaml:"env_file"`
	Discover    DiscoverConfig `yaml:"discover"`
	Groups      []Group        `yaml:"groups"`
}

// DiscoverConfig controls auto-discovery of services. All fields have
// sensible defaults; the struct is safe to leave empty.
type DiscoverConfig struct {
	Docker *bool                `yaml:"docker"`
	Dotnet DotnetDiscoverConfig `yaml:"dotnet"`
	NPM    NPMDiscoverConfig    `yaml:"npm"`
}

type DotnetDiscoverConfig struct {
	Enabled     *bool    `yaml:"enabled"`
	Glob        string   `yaml:"glob"`
	StripPrefix string   `yaml:"strip_prefix"`
	Exclude     []string `yaml:"exclude"`
}

type NPMDiscoverConfig struct {
	Enabled *bool    `yaml:"enabled"`
	Scripts []string `yaml:"scripts"`
}

type Group struct {
	Name     string        `yaml:"name"`
	Kind     string        `yaml:"kind"`
	Services []ServiceConf `yaml:"services"`
}

type ServiceConf struct {
	Name           string   `yaml:"name"`
	ComposeService string   `yaml:"compose_service"`
	Port           int      `yaml:"port"`
	Project        string   `yaml:"project"`
	Directory      string   `yaml:"directory"`
	InstallCommand string   `yaml:"install_command"`
	StartCommand   string   `yaml:"start_command"`
	DependsOn      []string `yaml:"depends_on"`
	PreStartCmd    string   `yaml:"pre_start_cmd"`
	PostStartCmd   string   `yaml:"post_start_cmd"`
}

// Load parses a YAML config file. project_root is resolved relative to the
// config file's directory.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	configDir := filepath.Dir(path)
	if cfg.ProjectRoot == "" {
		cfg.ProjectRoot = configDir
	} else {
		cfg.ProjectRoot = filepath.Clean(filepath.Join(configDir, cfg.ProjectRoot))
	}

	return &cfg, nil
}
