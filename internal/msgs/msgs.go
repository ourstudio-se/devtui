package msgs

import "time"

// ServiceState mirrors service.State to avoid circular imports.
type ServiceState int

const (
	StateStopped ServiceState = iota
	StateStarting
	StateRunning
	StateStopping
	StateBuilding
	StateError
	StateExternal
)

// StateChanged is sent when a service transitions state.
type StateChanged struct {
	ServiceName string
	NewState    ServiceState
	Error       error
}

// LogLine is sent when a new log line arrives from a service.
type LogLine struct {
	ServiceName string
	Line        string
	Timestamp   time.Time
}

// ProcessExited is sent when a managed process terminates.
type ProcessExited struct {
	ServiceName string
	ExitCode    int
	Error       error
}

// BuildDone is sent when the global dotnet solution build completes.
type BuildDone struct {
	Success bool
}

// ExternalServiceDetected is sent when a port is occupied by a process not managed by TUI.
type ExternalServiceDetected struct {
	ServiceName string
	PID         int
}

// ResourceStats holds resource usage for a single service.
type ResourceStats struct {
	CPUPercent float64
	MemUsageMB float64
	MemLimitMB float64
}

// ResourceUpdate delivers resource usage data for all running services.
type ResourceUpdate struct {
	Stats map[string]ResourceStats
}

// TickMsg for periodic status polling.
type TickMsg time.Time
