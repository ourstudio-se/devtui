package service

import (
	"sync"
)

type State int

const (
	StateStopped State = iota
	StateStarting
	StateRunning
	StateStopping
	StateBuilding
	StateError
	StateExternal
)

func (s State) String() string {
	switch s {
	case StateStopped:
		return "stopped"
	case StateStarting:
		return "starting"
	case StateRunning:
		return "running"
	case StateStopping:
		return "stopping"
	case StateBuilding:
		return "building"
	case StateError:
		return "error"
	case StateExternal:
		return "external"
	default:
		return "unknown"
	}
}

func (s State) Icon() string {
	switch s {
	case StateStopped:
		return "○"
	case StateStarting:
		return "◐"
	case StateRunning:
		return "●"
	case StateStopping:
		return "◑"
	case StateBuilding:
		return "◌"
	case StateError:
		return "✖"
	case StateExternal:
		return "●"
	default:
		return "?"
	}
}

type Kind string

const (
	KindDocker Kind = "docker"
	KindDotnet Kind = "dotnet"
	KindNPM    Kind = "npm"
)

type Service struct {
	Name         string
	Group        string
	Kind         Kind
	Port         int
	State        State
	Error        error
	DependsOn    []string
	PreStartCmd  string
	PostStartCmd string

	// Docker-specific
	ComposeService string

	// Dotnet-specific
	ProjectPath string

	// NPM-specific
	Directory      string
	InstallCommand string
	StartCommand   string

	// Runtime
	LogBuffer   *RingBuffer
	ExternalPID int
}

func NewService(name, group string, kind Kind) *Service {
	return &Service{
		Name:      name,
		Group:     group,
		Kind:      kind,
		State:     StateStopped,
		LogBuffer: NewRingBuffer(10000),
	}
}

// RingBuffer is a thread-safe circular buffer for log lines.
type RingBuffer struct {
	lines []string
	cap   int
	head  int
	count int
	mu    sync.Mutex
}

func NewRingBuffer(capacity int) *RingBuffer {
	return &RingBuffer{
		lines: make([]string, capacity),
		cap:   capacity,
	}
}

func (rb *RingBuffer) Write(line string) {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	rb.lines[rb.head] = line
	rb.head = (rb.head + 1) % rb.cap
	if rb.count < rb.cap {
		rb.count++
	}
}

func (rb *RingBuffer) Lines() []string {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	if rb.count == 0 {
		return nil
	}
	result := make([]string, rb.count)
	start := (rb.head - rb.count + rb.cap) % rb.cap
	for i := 0; i < rb.count; i++ {
		result[i] = rb.lines[(start+i)%rb.cap]
	}
	return result
}

func (rb *RingBuffer) Len() int {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	return rb.count
}

func (rb *RingBuffer) Clear() {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	rb.head = 0
	rb.count = 0
}
