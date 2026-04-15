package raft

import (
	"encoding/json"
	"fmt"
	"sync"
)

// CommandType represents the type of command in the FSM.
type CommandType int

const (
	CmdNoOp CommandType = iota
	CmdPoolConfigUpdate
	CmdUserCreate
	CmdUserUpdate
	CmdUserDelete
	CmdCacheInvalidate
	CmdCacheInvalidatePattern
	CmdBackendDetach
	CmdBackendAttach
)

// Command represents a command to be applied to the FSM.
type Command struct {
	Type CommandType     `json:"type"`
	Data json.RawMessage `json:"data"`
}

// FSM is a finite state machine that applies Raft log entries.
type FSM interface {
	// Apply applies a command to the FSM and returns the result.
	Apply(command Command) (interface{}, error)

	// Snapshot returns a snapshot of the current state.
	Snapshot() ([]byte, error)

	// Restore restores the FSM from a snapshot.
	Restore(snapshot []byte) error
}

// GeryonFSM implements the FSM for Geryon cluster state.
type GeryonFSM struct {
	mu     sync.RWMutex
	state  FSMState
	config FSMConfig
}

// FSMState represents the current state of the FSM.
type FSMState struct {
	Version     uint64                 `json:"version"`
	PoolConfigs map[string]interface{} `json:"pool_configs"`
	Users       map[string]FSMUser     `json:"users"`
	Backends    map[string]FSMBackend  `json:"backends"`
}

// FSMUser represents a user in the FSM state.
type FSMUser struct {
	Username       string   `json:"username"`
	PasswordHash   string   `json:"password_hash"`
	MaxConnections int      `json:"max_connections"`
	DefaultPool    string   `json:"default_pool"`
	AllowedPools   []string `json:"allowed_pools"`
}

// FSMBackend represents a backend in the FSM state.
type FSMBackend struct {
	Name     string `json:"name"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Role     string `json:"role"`
	Status   string `json:"status"`
	Detached bool   `json:"detached"`
}

// FSMConfig holds configuration for the FSM.
type FSMConfig struct {
	// Callbacks for state changes
	OnPoolConfigUpdate func(name string, config interface{})
	OnUserChange       func(username string, user *FSMUser, deleted bool)
	OnBackendChange    func(name string, backend *FSMBackend)
	OnCacheInvalidate  func(pattern string, tables []string)
}

// NewGeryonFSM creates a new Geryon FSM.
func NewGeryonFSM(config FSMConfig) *GeryonFSM {
	return &GeryonFSM{
		state: FSMState{
			Version:     1,
			PoolConfigs: make(map[string]interface{}),
			Users:       make(map[string]FSMUser),
			Backends:    make(map[string]FSMBackend),
		},
		config: config,
	}
}

// Apply applies a command to the FSM.
func (f *GeryonFSM) Apply(command Command) (interface{}, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.state.Version++

	switch command.Type {
	case CmdNoOp:
		return nil, nil

	case CmdPoolConfigUpdate:
		return f.applyPoolConfigUpdate(command.Data)

	case CmdUserCreate, CmdUserUpdate:
		return f.applyUserUpdate(command.Data)

	case CmdUserDelete:
		return f.applyUserDelete(command.Data)

	case CmdCacheInvalidate:
		return f.applyCacheInvalidate(command.Data)

	case CmdCacheInvalidatePattern:
		return f.applyCacheInvalidatePattern(command.Data)

	case CmdBackendDetach:
		return f.applyBackendDetach(command.Data)

	case CmdBackendAttach:
		return f.applyBackendAttach(command.Data)

	default:
		return nil, fmt.Errorf("unknown command type: %d", command.Type)
	}
}

// PoolConfigUpdateData represents pool config update data.
type PoolConfigUpdateData struct {
	Name   string      `json:"name"`
	Config interface{} `json:"config"`
	Delete bool        `json:"delete"`
}

func (f *GeryonFSM) applyPoolConfigUpdate(data json.RawMessage) (interface{}, error) {
	var update PoolConfigUpdateData
	if err := json.Unmarshal(data, &update); err != nil {
		return nil, fmt.Errorf("failed to unmarshal pool config update: %w", err)
	}

	if update.Delete {
		delete(f.state.PoolConfigs, update.Name)
	} else {
		f.state.PoolConfigs[update.Name] = update.Config
	}

	// Trigger callback
	if f.config.OnPoolConfigUpdate != nil {
		f.config.OnPoolConfigUpdate(update.Name, update.Config)
	}

	return nil, nil
}

// UserUpdateData represents user update data.
type UserUpdateData struct {
	User FSMUser `json:"user"`
}

func (f *GeryonFSM) applyUserUpdate(data json.RawMessage) (interface{}, error) {
	var update UserUpdateData
	if err := json.Unmarshal(data, &update); err != nil {
		return nil, fmt.Errorf("failed to unmarshal user update: %w", err)
	}

	f.state.Users[update.User.Username] = update.User

	// Trigger callback
	if f.config.OnUserChange != nil {
		f.config.OnUserChange(update.User.Username, &update.User, false)
	}

	return nil, nil
}

// UserDeleteData represents user delete data.
type UserDeleteData struct {
	Username string `json:"username"`
}

func (f *GeryonFSM) applyUserDelete(data json.RawMessage) (interface{}, error) {
	var del UserDeleteData
	if err := json.Unmarshal(data, &del); err != nil {
		return nil, fmt.Errorf("failed to unmarshal user delete: %w", err)
	}

	delete(f.state.Users, del.Username)

	// Trigger callback
	if f.config.OnUserChange != nil {
		f.config.OnUserChange(del.Username, nil, true)
	}

	return nil, nil
}

// CacheInvalidateData represents cache invalidation data.
type CacheInvalidateData struct {
	Tables []string `json:"tables"`
}

func (f *GeryonFSM) applyCacheInvalidate(data json.RawMessage) (interface{}, error) {
	var inv CacheInvalidateData
	if err := json.Unmarshal(data, &inv); err != nil {
		return nil, fmt.Errorf("failed to unmarshal cache invalidate: %w", err)
	}

	// Trigger callback
	if f.config.OnCacheInvalidate != nil {
		f.config.OnCacheInvalidate("", inv.Tables)
	}

	return nil, nil
}

// CacheInvalidatePatternData represents cache invalidation by pattern.
type CacheInvalidatePatternData struct {
	Pattern string `json:"pattern"`
}

func (f *GeryonFSM) applyCacheInvalidatePattern(data json.RawMessage) (interface{}, error) {
	var inv CacheInvalidatePatternData
	if err := json.Unmarshal(data, &inv); err != nil {
		return nil, fmt.Errorf("failed to unmarshal cache invalidate pattern: %w", err)
	}

	// Trigger callback
	if f.config.OnCacheInvalidate != nil {
		f.config.OnCacheInvalidate(inv.Pattern, nil)
	}

	return nil, nil
}

// BackendDetachData represents backend detach data.
type BackendDetachData struct {
	Name     string `json:"name"`
	PoolName string `json:"pool_name"`
}

func (f *GeryonFSM) applyBackendDetach(data json.RawMessage) (interface{}, error) {
	var detach BackendDetachData
	if err := json.Unmarshal(data, &detach); err != nil {
		return nil, fmt.Errorf("failed to unmarshal backend detach: %w", err)
	}

	if backend, ok := f.state.Backends[detach.Name]; ok {
		backend.Detached = true
		backend.Status = "detached"
		f.state.Backends[detach.Name] = backend

		// Trigger callback
		if f.config.OnBackendChange != nil {
			f.config.OnBackendChange(detach.Name, &backend)
		}
	}

	return nil, nil
}

// BackendAttachData represents backend attach data.
type BackendAttachData struct {
	Name     string `json:"name"`
	PoolName string `json:"pool_name"`
}

func (f *GeryonFSM) applyBackendAttach(data json.RawMessage) (interface{}, error) {
	var attach BackendAttachData
	if err := json.Unmarshal(data, &attach); err != nil {
		return nil, fmt.Errorf("failed to unmarshal backend attach: %w", err)
	}

	if backend, ok := f.state.Backends[attach.Name]; ok {
		backend.Detached = false
		backend.Status = "active"
		f.state.Backends[attach.Name] = backend

		// Trigger callback
		if f.config.OnBackendChange != nil {
			f.config.OnBackendChange(attach.Name, &backend)
		}
	}

	return nil, nil
}

// Snapshot returns a snapshot of the current state.
func (f *GeryonFSM) Snapshot() ([]byte, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	return json.Marshal(f.state)
}

// Restore restores the FSM from a snapshot.
func (f *GeryonFSM) Restore(snapshot []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	var state FSMState
	if err := json.Unmarshal(snapshot, &state); err != nil {
		return fmt.Errorf("failed to unmarshal snapshot: %w", err)
	}

	f.state = state
	return nil
}

// GetState returns a copy of the current state.
func (f *GeryonFSM) GetState() FSMState {
	f.mu.RLock()
	defer f.mu.RUnlock()

	// Deep copy
	stateCopy := FSMState{
		Version:     f.state.Version,
		PoolConfigs: make(map[string]interface{}),
		Users:       make(map[string]FSMUser),
		Backends:    make(map[string]FSMBackend),
	}

	for k, v := range f.state.PoolConfigs {
		stateCopy.PoolConfigs[k] = v
	}
	for k, v := range f.state.Users {
		stateCopy.Users[k] = v
	}
	for k, v := range f.state.Backends {
		stateCopy.Backends[k] = v
	}

	return stateCopy
}

// CreateCommand creates a command with the given type and data.
func CreateCommand(cmdType CommandType, data interface{}) (Command, error) {
	dataBytes, err := json.Marshal(data)
	if err != nil {
		return Command{}, fmt.Errorf("failed to marshal command data: %w", err)
	}

	return Command{
		Type: cmdType,
		Data: dataBytes,
	}, nil
}
