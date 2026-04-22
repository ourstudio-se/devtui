package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

type ServiceEntry struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
	PID  int    `json:"pid"`
	PGID int    `json:"pgid"`
	Port int    `json:"port"`
}

type StateFile struct {
	Services []ServiceEntry `json:"services"`
}

func stateDir(projectRoot string) string {
	return filepath.Join(projectRoot, ".devtui")
}

func statePath(projectRoot string) string {
	return filepath.Join(stateDir(projectRoot), "state.json")
}

func logDir(projectRoot string) string {
	return filepath.Join(stateDir(projectRoot), "logs")
}

func logFilePath(projectRoot, serviceName string) string {
	safe := strings.NewReplacer("/", "_", "\\", "_", " ", "_").Replace(serviceName)
	return filepath.Join(logDir(projectRoot), safe+".log")
}

func ensureDirs(projectRoot string) {
	os.MkdirAll(logDir(projectRoot), 0755)
}

func LoadState(projectRoot string) (*StateFile, error) {
	data, err := os.ReadFile(statePath(projectRoot))
	if err != nil {
		return &StateFile{}, nil
	}
	var sf StateFile
	if err := json.Unmarshal(data, &sf); err != nil {
		return &StateFile{}, nil
	}
	return &sf, nil
}

func SaveState(projectRoot string, sf *StateFile) error {
	ensureDirs(projectRoot)
	data, err := json.MarshalIndent(sf, "", "  ")
	if err != nil {
		return err
	}
	tmp := statePath(projectRoot) + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, statePath(projectRoot))
}

func (m *Manager) saveCurrentState() {
	var entries []ServiceEntry
	for name, proc := range m.processes {
		pid := proc.pid
		pgid := proc.pgid
		if proc.cmd != nil && proc.cmd.Process != nil {
			pid = proc.cmd.Process.Pid
			pgid = pid
		}
		if pid == 0 {
			continue
		}
		entry := ServiceEntry{
			Name: name,
			PID:  pid,
			PGID: pgid,
		}
		entries = append(entries, entry)
	}
	SaveState(m.projectRoot, &StateFile{Services: entries})
}

func (m *Manager) removeServiceFromState(name string) {
	sf, _ := LoadState(m.projectRoot)
	var filtered []ServiceEntry
	for _, e := range sf.Services {
		if e.Name != name {
			filtered = append(filtered, e)
		}
	}
	SaveState(m.projectRoot, &StateFile{Services: filtered})
}
