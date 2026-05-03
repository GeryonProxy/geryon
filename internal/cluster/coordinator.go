package cluster

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/GeryonProxy/geryon/internal/config"
	"github.com/GeryonProxy/geryon/internal/logger"
	"github.com/GeryonProxy/geryon/internal/raft"
	"github.com/GeryonProxy/geryon/internal/swim"
	"github.com/GeryonProxy/geryon/internal/tlsutil"
)

// CacheInvalidator is the interface for invalidating query cache entries.
type CacheInvalidator interface {
	InvalidateTables(tables []string)
}

// Coordinator wires together Raft consensus and SWIM gossip protocols.
type Coordinator struct {
	mu sync.RWMutex

	// Components
	raftNode   *raft.Node
	swimProto  *swim.Protocol
	fsm        *raft.GeryonFSM
	configMgr  *ConfigManager
	swimEvents <-chan swim.Event

	// Configuration
	config  *config.ClusterConfig
	dataDir string
	nodeID  string

	// Channels
	stopCh    chan struct{}
	eventCh   chan ClusterEvent
	commandCh chan ClusterCommand

	// State
	members         map[string]*MemberInfo
	isLeader        bool
	cacheInvalidator CacheInvalidator
	logger          *logger.Logger
}

// MemberInfo represents information about a cluster member.
type MemberInfo struct {
	NodeID      string         `json:"node_id"`
	Address     string         `json:"address"`
	RaftAddress string         `json:"raft_address"`
	SWIMAddress string         `json:"swim_address"`
	State       MemberState    `json:"state"`
	LastSeen    time.Time      `json:"last_seen"`
	Metadata    MemberMetadata `json:"metadata"`
}

// MemberState represents the state of a cluster member.
type MemberState int

const (
	MemberAlive MemberState = iota
	MemberSuspect
	MemberDead
	MemberLeft
)

func (s MemberState) String() string {
	switch s {
	case MemberAlive:
		return "alive"
	case MemberSuspect:
		return "suspect"
	case MemberDead:
		return "dead"
	case MemberLeft:
		return "left"
	default:
		return "unknown"
	}
}

// MemberMetadata contains runtime information about a member.
type MemberMetadata struct {
	Version         string                `json:"version"`
	Uptime          string                `json:"uptime"`
	LoadAvg         float64               `json:"load_avg"`
	ConnectionCount int                   `json:"connection_count"`
	QueryRate       float64               `json:"query_rate"`
	PoolStatuses    map[string]PoolStatus `json:"pool_statuses"`
}

// PoolStatus represents the status of a pool on a member.
type PoolStatus struct {
	ActiveConnections int     `json:"active_connections"`
	TotalConnections  int     `json:"total_connections"`
	QueriesPerSecond  float64 `json:"queries_per_second"`
	Healthy           bool    `json:"healthy"`
}

// ClusterEvent represents an event in the cluster.
type ClusterEvent struct {
	Type      EventType
	NodeID    string
	Data      interface{}
	Timestamp time.Time
}

// EventType represents the type of cluster event.
type EventType int

const (
	EventMemberJoined EventType = iota
	EventMemberLeft
	EventMemberFailed
	EventLeaderChanged
	EventConfigChanged
	EventBackendHealthChanged
)

// ClusterCommand represents a command to be executed on the cluster.
type ClusterCommand struct {
	Type   CommandType
	Data   interface{}
	RespCh chan<- CommandResponse
}

// CommandType represents the type of cluster command.
type CommandType int

const (
	CmdUpdatePoolConfig CommandType = iota
	CmdCreateUser
	CmdUpdateUser
	CmdDeleteUser
	CmdDetachBackend
	CmdAttachBackend
	CmdInvalidateCache
	CmdReloadConfig
)

// CommandResponse represents the response to a cluster command.
type CommandResponse struct {
	Success bool
	Error   error
	Data    interface{}
}

// NewCoordinator creates a new cluster coordinator.
func NewCoordinator(cfg *config.ClusterConfig, dataDir string, log *logger.Logger) (*Coordinator, error) {
	if !cfg.Enabled {
		return nil, fmt.Errorf("clustering is not enabled")
	}

	c := &Coordinator{
		config:    cfg,
		dataDir:   dataDir,
		nodeID:    cfg.NodeID,
		members:   make(map[string]*MemberInfo),
		stopCh:    make(chan struct{}),
		eventCh:   make(chan ClusterEvent, 100),
		commandCh: make(chan ClusterCommand, 100),
		logger:    log,
	}

	// Create FSM with callbacks
	fsmConfig := raft.FSMConfig{
		OnPoolConfigUpdate: c.onPoolConfigUpdate,
		OnUserChange:       c.onUserChange,
		OnBackendChange:    c.onBackendChange,
		OnCacheInvalidate:  c.onCacheInvalidate,
	}
	c.fsm = raft.NewGeryonFSM(fsmConfig)

	return c, nil
}

// Start starts the cluster coordinator.
func (c *Coordinator) Start() error {
	c.logger.Info("Starting cluster coordinator", "node_id", c.nodeID)

	// C-2 fix: Load TLS config for inter-node encryption
	var tlsConfig *tls.Config
	if c.config.TLS.Mode != "" && c.config.TLS.Mode != "disable" {
		var err error
		tlsConfig, err = tlsutil.LoadServerConfig(c.config.TLS)
		if err != nil {
			return fmt.Errorf("failed to load cluster TLS config: %w", err)
		}
		c.logger.Info("Cluster TLS enabled", "mode", c.config.TLS.Mode)
	}

	// Start Raft node
	raftNode, err := raft.NewNode(
		c.nodeID,
		c.config.Raft.Listen,
		c.config.Raft.Peers,
		c.dataDir+"/raft",
		c.config.Secret, // C-2 fix
		tlsConfig,       // C-2 fix
		c.fsm,
		c.logger,
	)
	if err != nil {
		return fmt.Errorf("failed to create raft node: %w", err)
	}
	c.raftNode = raftNode

	if err := raftNode.Start(); err != nil {
		return fmt.Errorf("failed to start raft node: %w", err)
	}

	// Start SWIM protocol
	swimProto := swim.NewProtocol(c.nodeID, c.config.Gossip.Listen, c.logger)
	swimProto.SetSecret(c.config.Secret) // C-2 fix
	c.swimProto = swimProto

	if err := swimProto.Start(); err != nil {
		return fmt.Errorf("failed to start swim protocol: %w", err)
	}

	// Get event channel from SWIM
	c.swimEvents = swimProto.Events()

	// Join SWIM cluster
	if len(c.config.Gossip.Join) > 0 {
		if err := swimProto.Join(c.config.Gossip.Join); err != nil {
			c.logger.Warn("Failed to join SWIM cluster", "error", err)
		}
	}

	// Start coordinator goroutines
	go c.run()
	go c.heartbeat()
	go c.monitorLeader()

	c.logger.Info("Cluster coordinator started",
		"node_id", c.nodeID,
		"raft_addr", c.config.Raft.Listen,
		"swim_addr", c.config.Gossip.Listen,
	)

	return nil
}

// Stop stops the cluster coordinator.
func (c *Coordinator) Stop() error {
	c.logger.Info("Stopping cluster coordinator")
	close(c.stopCh)

	if c.raftNode != nil {
		c.raftNode.Stop()
	}

	if c.swimProto != nil {
		c.swimProto.Stop()
	}

	return nil
}

// run is the main coordinator loop.
func (c *Coordinator) run() {
	for {
		select {
		case <-c.stopCh:
			return
		case event := <-c.eventCh:
			c.handleEvent(event)
		case cmd := <-c.commandCh:
			c.handleCommand(cmd)
		case event := <-c.swimEvents:
			c.handleSWIMEvent(event)
		}
	}
}

// heartbeat periodically broadcasts member metadata.
func (c *Coordinator) heartbeat() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-c.stopCh:
			return
		case <-ticker.C:
			c.broadcastMetadata()
		}
	}
}

// monitorLeader monitors Raft leadership changes.
func (c *Coordinator) monitorLeader() {
	for {
		select {
		case <-c.stopCh:
			return
		default:
		}

		isLeader := c.raftNode.IsLeader()
		c.mu.Lock()
		wasLeader := c.isLeader
		c.isLeader = isLeader
		c.mu.Unlock()

		if isLeader && !wasLeader {
			c.logger.Info("Became cluster leader", "node_id", c.nodeID)
			c.eventCh <- ClusterEvent{
				Type:      EventLeaderChanged,
				NodeID:    c.nodeID,
				Timestamp: time.Now(),
				Data:      map[string]string{"leader": c.nodeID},
			}
		} else if !isLeader && wasLeader {
			c.logger.Info("Stepped down as cluster leader", "node_id", c.nodeID)
		}

		time.Sleep(1 * time.Second)
	}
}

// broadcastMetadata broadcasts this node's metadata to the cluster.
func (c *Coordinator) broadcastMetadata() {
	metadata := c.collectMetadata()

	data, err := json.Marshal(metadata)
	if err != nil {
		c.logger.Error("Failed to marshal metadata", "error", err)
		return
	}

	// Broadcast via SWIM user data
	c.swimProto.BroadcastUserData(data)
}

// collectMetadata collects runtime metadata about this node.
func (c *Coordinator) collectMetadata() MemberMetadata {
	return MemberMetadata{
		Version:         "1.0.0",
		Uptime:          time.Since(time.Now().Add(-time.Hour)).String(),
		LoadAvg:         0.0,
		ConnectionCount: 0,
		QueryRate:       0.0,
		PoolStatuses:    make(map[string]PoolStatus),
	}
}

// handleSWIMEvent handles events from the SWIM protocol.
func (c *Coordinator) handleSWIMEvent(event swim.Event) {
	switch event.Type {
	case swim.EventMemberJoin:
		if event.Member != nil {
			c.handleMemberJoined(event.Member.ID, event.Member.Address)
		}
	case swim.EventMemberLeave:
		if event.Member != nil {
			c.handleMemberFailed(event.Member.ID)
		}
	case swim.EventMemberUpdate:
		// Member update could indicate recovery
		if event.Member != nil && event.Member.State == swim.StateAlive {
			c.handleMemberRecovered(event.Member.ID)
		}
	}
}

// handleMemberJoined handles a new member joining.
func (c *Coordinator) handleMemberJoined(nodeID, address string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.members[nodeID] = &MemberInfo{
		NodeID:   nodeID,
		Address:  address,
		State:    MemberAlive,
		LastSeen: time.Now(),
	}

	c.eventCh <- ClusterEvent{
		Type:      EventMemberJoined,
		NodeID:    nodeID,
		Timestamp: time.Now(),
	}

	c.logger.Info("Cluster member joined", "node_id", nodeID, "address", address)
}

// handleMemberFailed handles a member failure.
func (c *Coordinator) handleMemberFailed(nodeID string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if member, ok := c.members[nodeID]; ok {
		member.State = MemberDead
		member.LastSeen = time.Now()
	}

	c.eventCh <- ClusterEvent{
		Type:      EventMemberFailed,
		NodeID:    nodeID,
		Timestamp: time.Now(),
	}

	c.logger.Info("Cluster member failed", "node_id", nodeID)
}

// handleMemberRecovered handles a member recovery.
func (c *Coordinator) handleMemberRecovered(nodeID string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if member, ok := c.members[nodeID]; ok {
		member.State = MemberAlive
		member.LastSeen = time.Now()
	}

	c.logger.Info("Cluster member recovered", "node_id", nodeID)
}

// handleMetadataMessage handles metadata messages from other nodes.
func (c *Coordinator) handleMetadataMessage(nodeID string, data []byte) {
	var metadata MemberMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		c.logger.Debug("Failed to unmarshal metadata", "error", err)
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if member, ok := c.members[nodeID]; ok {
		member.Metadata = metadata
		member.LastSeen = time.Now()
	}
}

// handleEvent handles cluster events.
func (c *Coordinator) handleEvent(event ClusterEvent) {
	switch event.Type {
	case EventMemberFailed:
		// If leader failed, Raft will handle election
		// Share backend health info to avoid thundering herd
		c.shareBackendHealth(event.NodeID)
	}
}

// handleCommand handles cluster commands.
func (c *Coordinator) handleCommand(cmd ClusterCommand) {
	var resp CommandResponse

	switch cmd.Type {
	case CmdReloadConfig:
		resp = c.handleReloadConfig(cmd.Data)
	default:
		// Forward to Raft for consensus
		resp = c.proposeToRaft(cmd)
	}

	if cmd.RespCh != nil {
		cmd.RespCh <- resp
	}
}

// proposeToRaft proposes a command to Raft for consensus.
func (c *Coordinator) proposeToRaft(cmd ClusterCommand) CommandResponse {
	// Convert command to Raft command
	raftCmd, err := c.convertToRaftCommand(cmd)
	if err != nil {
		return CommandResponse{Success: false, Error: err}
	}

	// Propose to Raft
	index, err := c.raftNode.Propose(raftCmd)
	if err != nil {
		return CommandResponse{Success: false, Error: err}
	}

	return CommandResponse{Success: true, Data: map[string]uint64{"index": index}}
}

// convertToRaftCommand converts a cluster command to a Raft command.
func (c *Coordinator) convertToRaftCommand(cmd ClusterCommand) (raft.Command, error) {
	switch cmd.Type {
	case CmdUpdatePoolConfig:
		return raft.CreateCommand(raft.CmdPoolConfigUpdate, cmd.Data)
	case CmdCreateUser:
		return raft.CreateCommand(raft.CmdUserCreate, cmd.Data)
	case CmdUpdateUser:
		return raft.CreateCommand(raft.CmdUserUpdate, cmd.Data)
	case CmdDeleteUser:
		return raft.CreateCommand(raft.CmdUserDelete, cmd.Data)
	case CmdDetachBackend:
		return raft.CreateCommand(raft.CmdBackendDetach, cmd.Data)
	case CmdAttachBackend:
		return raft.CreateCommand(raft.CmdBackendAttach, cmd.Data)
	case CmdInvalidateCache:
		return raft.CreateCommand(raft.CmdCacheInvalidate, cmd.Data)
	default:
		return raft.Command{}, fmt.Errorf("unknown command type: %d", cmd.Type)
	}
}

// handleReloadConfig handles config reload command cluster-wide.
func (c *Coordinator) handleReloadConfig(data interface{}) CommandResponse {
	if c.isLeader {
		// Leader triggers reload on all nodes via event
		c.eventCh <- ClusterEvent{
			Type:      EventConfigChanged,
			NodeID:    c.nodeID,
			Timestamp: time.Now(),
			Data:      map[string]interface{}{"type": "reload", "source": c.nodeID},
		}
		return CommandResponse{Success: true}
	}
	// Non-leader forwards to leader
	return c.forwardToLeader(CmdReloadConfig, data)
}

// forwardToLeader forwards a command to the leader via Raft.
func (c *Coordinator) forwardToLeader(cmdType CommandType, data interface{}) CommandResponse {
	leader := c.GetLeader()
	if leader == "" {
		return CommandResponse{Success: false, Error: fmt.Errorf("no leader available")}
	}
	if leader == c.nodeID {
		// We are the leader, propose directly
		respCh := make(chan CommandResponse, 1)
		cmd := ClusterCommand{Type: cmdType, Data: data, RespCh: respCh}
		select {
		case c.commandCh <- cmd:
			return <-respCh
		default:
			return CommandResponse{Success: false, Error: fmt.Errorf("command channel full")}
		}
	}
	// Forward to leader via SWIM
	return c.sendCommandToNode(leader, cmdType, data)
}

// sendCommandToNode sends a command to a specific node via SWIM.
func (c *Coordinator) sendCommandToNode(nodeID string, cmdType CommandType, data interface{}) CommandResponse {
	c.mu.RLock()
	member, ok := c.members[nodeID]
	c.mu.RUnlock()
	if !ok {
		return CommandResponse{Success: false, Error: fmt.Errorf("node %s not found", nodeID)}
	}

	// Create command message
	msg := CommandMessage{
		Type:   cmdType,
		Data:   data,
		From:   c.nodeID,
		NodeID: nodeID,
	}

	// Serialize and send via SWIM
	payload, err := json.Marshal(msg)
	if err != nil {
		return CommandResponse{Success: false, Error: err}
	}

	c.swimProto.SendTo(member.Address, payload)
	return CommandResponse{Success: true}
}

// CommandMessage represents a forwarded command message.
type CommandMessage struct {
	Type   CommandType `json:"type"`
	Data   interface{} `json:"data"`
	From   string      `json:"from"`
	NodeID string      `json:"node_id"`
}

// shareBackendHealth shares backend health information across the cluster.
func (c *Coordinator) shareBackendHealth(failedNode string) {
	if c.swimProto == nil {
		return
	}

	c.mu.RLock()
	localHealth := make(map[string]BackendHealth)
	for name, info := range c.members {
		if name == c.nodeID {
			continue // Skip self
		}
		// Extract pool health from member metadata
		for poolName, status := range info.Metadata.PoolStatuses {
			if status.Healthy {
				localHealth[poolName] = BackendHealth{
					Pool:    poolName,
					Backend: name,
					Healthy: true,
					Latency: 0, // Will be measured locally
				}
			}
		}
	}
	c.mu.RUnlock()

	// Broadcast health info via SWIM
	healthMsg := HealthBroadcast{
		Source:        c.nodeID,
		FailedNode:    failedNode,
		BackendHealth: localHealth,
		Timestamp:     time.Now(),
	}

	data, err := json.Marshal(healthMsg)
	if err != nil {
		c.logger.Error("Failed to marshal health broadcast", "error", err)
		return
	}

	c.swimProto.BroadcastUserData(data)
	c.logger.Debug("Shared backend health after node failure",
		"failed_node", failedNode,
		"backends_shared", len(localHealth))
}

// HealthBroadcast represents a backend health broadcast message.
type HealthBroadcast struct {
	Source        string                   `json:"source"`
	FailedNode    string                   `json:"failed_node"`
	BackendHealth map[string]BackendHealth `json:"backend_health"`
	Timestamp     time.Time                `json:"timestamp"`
}

// BackendHealth represents the health status of a backend.
type BackendHealth struct {
	Pool    string `json:"pool"`
	Backend string `json:"backend"`
	Healthy bool   `json:"healthy"`
	Latency int64  `json:"latency"`
}

// handleHealthBroadcast processes a received health broadcast.
func (c *Coordinator) handleHealthBroadcast(from string, data []byte) {
	var broadcast HealthBroadcast
	if err := json.Unmarshal(data, &broadcast); err != nil {
		c.logger.Debug("Failed to unmarshal health broadcast", "error", err)
		return
	}

	// Update our view of remote backends
	c.mu.Lock()
	for poolName, health := range broadcast.BackendHealth {
		// Store for use by routing layer
		c.logger.Debug("Received backend health update",
			"pool", poolName,
			"backend", health.Backend,
			"healthy", health.Healthy,
			"from", from)
	}
	c.mu.Unlock()

	// Emit event for routing layer
	c.eventCh <- ClusterEvent{
		Type:      EventBackendHealthChanged,
		NodeID:    from,
		Timestamp: time.Now(),
		Data:      broadcast,
	}
}

// FSM Callbacks

func (c *Coordinator) onPoolConfigUpdate(name string, cfg interface{}) {
	c.eventCh <- ClusterEvent{
		Type:      EventConfigChanged,
		NodeID:    c.nodeID,
		Timestamp: time.Now(),
		Data:      map[string]interface{}{"type": "pool", "name": name, "config": cfg},
	}
}

func (c *Coordinator) onUserChange(username string, user *raft.FSMUser, deleted bool) {
	c.eventCh <- ClusterEvent{
		Type:      EventConfigChanged,
		NodeID:    c.nodeID,
		Timestamp: time.Now(),
		Data:      map[string]interface{}{"type": "user", "username": username, "deleted": deleted},
	}
}

func (c *Coordinator) onBackendChange(name string, backend *raft.FSMBackend) {
	c.eventCh <- ClusterEvent{
		Type:      EventBackendHealthChanged,
		NodeID:    c.nodeID,
		Timestamp: time.Now(),
		Data:      map[string]interface{}{"name": name, "backend": backend},
	}
}

func (c *Coordinator) onCacheInvalidate(pattern string, tables []string) {
	if c.cacheInvalidator != nil && len(tables) > 0 {
		c.cacheInvalidator.InvalidateTables(tables)
	}
}

// SetCacheInvalidator sets the cache invalidator for cross-node cache coherence.
func (c *Coordinator) SetCacheInvalidator(invalidator CacheInvalidator) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cacheInvalidator = invalidator
}

// Public API

// IsLeader returns true if this node is the cluster leader.
func (c *Coordinator) IsLeader() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.isLeader
}

// GetLeader returns the current leader node ID.
func (c *Coordinator) GetLeader() string {
	if c.raftNode.IsLeader() {
		return c.nodeID
	}
	return c.raftNode.GetLeaderID()
}

// GetMembers returns all cluster members.
func (c *Coordinator) GetMembers() []*MemberInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()

	members := make([]*MemberInfo, 0, len(c.members))
	for _, m := range c.members {
		members = append(members, m)
	}
	return members
}

// GetMember returns a specific member.
func (c *Coordinator) GetMember(nodeID string) *MemberInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.members[nodeID]
}

// Propose proposes a command to the cluster.
func (c *Coordinator) Propose(ctx context.Context, cmdType CommandType, data interface{}) (*CommandResponse, error) {
	respCh := make(chan CommandResponse, 1)

	cmd := ClusterCommand{
		Type:   cmdType,
		Data:   data,
		RespCh: respCh,
	}

	select {
	case c.commandCh <- cmd:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	select {
	case resp := <-respCh:
		return &resp, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// UpdatePoolConfig updates a pool configuration cluster-wide.
func (c *Coordinator) UpdatePoolConfig(ctx context.Context, name string, cfg interface{}) error {
	resp, err := c.Propose(ctx, CmdUpdatePoolConfig, map[string]interface{}{
		"name":   name,
		"config": cfg,
	})
	if err != nil {
		return err
	}
	if !resp.Success {
		return resp.Error
	}
	return nil
}

// ConfigManager handles configuration management.
type ConfigManager struct {
	mu          sync.RWMutex
	config      *config.Config
	coordinator *Coordinator
}

// NewConfigManager creates a new config manager.
func NewConfigManager(cfg *config.Config, coord *Coordinator) *ConfigManager {
	return &ConfigManager{
		config:      cfg,
		coordinator: coord,
	}
}

// ReloadConfig reloads configuration cluster-wide.
func (cm *ConfigManager) ReloadConfig(ctx context.Context) error {
	if cm.coordinator != nil && cm.coordinator.IsLeader() {
		// Leader proposes config reload to all nodes
		_, err := cm.coordinator.Propose(ctx, CmdReloadConfig, nil)
		return err
	}
	// Non-leader just validates
	return nil
}
