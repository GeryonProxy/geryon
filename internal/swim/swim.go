package swim

import (
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/GeryonProxy/geryon/internal/logger"
)

// Maximum gossip message size (64KB, matches receive buffer)
const maxGossipMessageSize = 65536

// Protocol implements the SWIM gossip protocol for node discovery.
type Protocol struct {
	mu       sync.RWMutex
	id       string
	addr     string
	members  map[string]*Member
	seqNum   atomic.Uint64

	// Configuration
	probeInterval   time.Duration
	probeTimeout    time.Duration
	suspectTimeout  time.Duration
	syncInterval    time.Duration

	// Channels
	stopCh          chan struct{}
	eventCh         chan Event

	// Networking
	listener        net.PacketConn

	// Logger
	logger          *logger.Logger
}

// Member represents a cluster member.
type Member struct {
	ID          string
	Address     string
	State       MemberState
	Incarnation uint64
	LastSeen    time.Time
	LastProbe   time.Time
}

// MemberState represents the state of a member.
type MemberState int

const (
	StateAlive MemberState = iota
	StateSuspect
	StateDead
)

func (s MemberState) String() string {
	switch s {
	case StateAlive:
		return "Alive"
	case StateSuspect:
		return "Suspect"
	case StateDead:
		return "Dead"
	default:
		return "Unknown"
	}
}

// Event represents a membership event.
type Event struct {
	Type   EventType
	Member *Member
}

// EventType represents the type of membership event.
type EventType int

const (
	EventMemberJoin EventType = iota
	EventMemberLeave
	EventMemberUpdate
)

// Message represents a SWIM message.
type Message struct {
	Type        MessageType  `json:"type"`
	Source      string       `json:"source"`
	Target      string       `json:"target"`
	Incarnation uint64       `json:"incarnation"`
	Payload     []byte       `json:"payload"`
	Members     []MemberInfo `json:"members,omitempty"`
}

// MessageType represents the type of SWIM message.
type MessageType int

const (
	MsgPing MessageType = iota
	MsgAck
	MsgPingReq
	MsgSuspect
	MsgAlive
	MsgDead
	MsgSync
)

// MemberInfo represents member information in gossip.
type MemberInfo struct {
	ID          string      `json:"id"`
	Address     string      `json:"address"`
	State       MemberState `json:"state"`
	Incarnation uint64      `json:"incarnation"`
}

// NewProtocol creates a new SWIM protocol instance.
func NewProtocol(id, addr string, log *logger.Logger) *Protocol {
	return &Protocol{
		id:             id,
		addr:           addr,
		members:        make(map[string]*Member),
		probeInterval:  1 * time.Second,
		probeTimeout:   500 * time.Millisecond,
		suspectTimeout: 5 * time.Second,
		syncInterval:   10 * time.Second,
		stopCh:         make(chan struct{}),
		eventCh:        make(chan Event, 100),
		logger:         log,
	}
}

// Start starts the SWIM protocol.
func (p *Protocol) Start() error {
	// Start UDP listener
	addr, err := net.ResolveUDPAddr("udp", p.addr)
	if err != nil {
		return fmt.Errorf("failed to resolve address: %w", err)
	}

	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return fmt.Errorf("failed to start listener: %w", err)
	}
	p.listener = conn

	p.logger.Info("SWIM protocol starting",
		"id", p.id,
		"addr", p.addr,
	)

	// Start background tasks
	go p.receiveLoop()
	go p.probeLoop()
	go p.suspectLoop()
	go p.syncLoop()

	return nil
}

// Stop stops the SWIM protocol.
func (p *Protocol) Stop() error {
	close(p.stopCh)
	if p.listener != nil {
		p.listener.Close()
	}
	return nil
}

// Join joins an existing cluster.
func (p *Protocol) Join(seeds []string) error {
	for _, seed := range seeds {
		if seed == p.addr {
			continue
		}

		p.logger.Info("Joining cluster via seed", "seed", seed)

		// Send sync request
		msg := Message{
			Type:    MsgSync,
			Source:  p.id,
			Payload: []byte(p.addr),
		}

		if err := p.sendMessage(seed, msg); err != nil {
			p.logger.Debug("Failed to contact seed", "seed", seed, "error", err)
			continue
		}

		return nil
	}

	return fmt.Errorf("failed to join cluster via any seed")
}

// Members returns a list of alive members.
func (p *Protocol) Members() []*Member {
	p.mu.RLock()
	defer p.mu.RUnlock()

	members := make([]*Member, 0)
	for _, m := range p.members {
		if m.State == StateAlive {
			members = append(members, m)
		}
	}
	return members
}

// MemberCount returns the number of members.
func (p *Protocol) MemberCount() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.members)
}

// Events returns the event channel.
func (p *Protocol) Events() <-chan Event {
	return p.eventCh
}

// receiveLoop receives incoming messages.
func (p *Protocol) receiveLoop() {
	buf := make([]byte, 65536)

	for {
		n, addr, err := p.listener.ReadFrom(buf)
		if err != nil {
			if p.isStopping() {
				return
			}
			p.logger.Debug("Failed to read packet", "error", err)
			continue
		}

		// Bound message size to prevent excessive memory allocation
		if n > maxGossipMessageSize {
			p.logger.Debug("Gossip message too large", "size", n, "from", addr)
			continue
		}

		var msg Message
		if err := json.Unmarshal(buf[:n], &msg); err != nil {
			p.logger.Debug("Failed to unmarshal message", "error", err)
			continue
		}

		p.handleMessage(msg, addr.String())
	}
}

// handleMessage processes an incoming message.
func (p *Protocol) handleMessage(msg Message, from string) {
	switch msg.Type {
	case MsgPing:
		p.handlePing(msg, from)
	case MsgAck:
		p.handleAck(msg)
	case MsgPingReq:
		p.handlePingReq(msg)
	case MsgSuspect:
		p.handleSuspect(msg)
	case MsgAlive:
		p.handleAlive(msg)
	case MsgDead:
		p.handleDead(msg)
	case MsgSync:
		p.handleSync(msg)
	}
}

// handlePing handles a ping message.
func (p *Protocol) handlePing(msg Message, from string) {
	// Update member info
	p.updateMember(msg.Source, from, StateAlive, msg.Incarnation)

	// Send ack
	ack := Message{
		Type:        MsgAck,
		Source:      p.id,
		Target:      msg.Source,
		Incarnation: p.incarnation(),
	}
	p.sendMessage(from, ack)
}

// handleAck handles an ack message.
func (p *Protocol) handleAck(msg Message) {
	p.mu.Lock()
	member, exists := p.members[msg.Source]
	if exists {
		member.LastSeen = time.Now()
		member.State = StateAlive
	}
	p.mu.Unlock()
}

// handlePingReq handles an indirect ping request.
func (p *Protocol) handlePingReq(msg Message) {
	// Forward ping to target
	var payload struct {
		Target string `json:"target"`
	}
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		return
	}

	// Try to ping the target
	ping := Message{
		Type:   MsgPing,
		Source: p.id,
	}
	p.sendMessage(payload.Target, ping)
}

// handleSuspect handles a suspect message.
func (p *Protocol) handleSuspect(msg Message) {
	p.mu.Lock()
	member, exists := p.members[msg.Target]
	if exists && member.Incarnation <= msg.Incarnation {
		member.State = StateSuspect
	}
	p.mu.Unlock()
}

// handleAlive handles an alive message.
func (p *Protocol) handleAlive(msg Message) {
	var payload struct {
		Address string `json:"address"`
	}
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		return
	}

	p.updateMember(msg.Source, payload.Address, StateAlive, msg.Incarnation)
}

// handleDead handles a dead message.
func (p *Protocol) handleDead(msg Message) {
	p.mu.Lock()
	member, exists := p.members[msg.Target]
	if exists && member.Incarnation <= msg.Incarnation {
		member.State = StateDead
	}
	p.mu.Unlock()
}

// handleSync handles a sync message (join request).
func (p *Protocol) handleSync(msg Message) {
	// Add the new member
	addr := string(msg.Payload)
	p.updateMember(msg.Source, addr, StateAlive, msg.Incarnation)

	// Send our member list
	members := p.getMemberInfos()
	resp := Message{
		Type:    MsgSync,
		Source:  p.id,
		Members: members,
	}
	p.sendMessage(addr, resp)
}

// probeLoop periodically probes random members.
func (p *Protocol) probeLoop() {
	ticker := time.NewTicker(p.probeInterval)
	defer ticker.Stop()

	for {
		select {
		case <-p.stopCh:
			return
		case <-ticker.C:
			p.probeRandomMember()
		}
	}
}

// probeRandomMember probes a random member.
func (p *Protocol) probeRandomMember() {
	member := p.selectRandomMember()
	if member == nil || member.ID == p.id {
		return
	}

	// Send ping
	ping := Message{
		Type:   MsgPing,
		Source: p.id,
	}

	member.LastProbe = time.Now()
	p.sendMessage(member.Address, ping)

	// Wait for ack with timeout
	time.AfterFunc(p.probeTimeout, func() {
		// Check if we got an ack
		p.mu.RLock()
		m, exists := p.members[member.ID]
		if exists && m.LastProbe.Equal(member.LastProbe) {
			// No ack received, mark as suspect
			p.mu.RUnlock()
			p.suspectMember(member.ID)
		} else {
			p.mu.RUnlock()
		}
	})
}

// suspectMember marks a member as suspect.
func (p *Protocol) suspectMember(memberID string) {
	p.mu.Lock()
	member, exists := p.members[memberID]
	if !exists || member.State != StateAlive {
		p.mu.Unlock()
		return
	}

	member.State = StateSuspect
	p.mu.Unlock()

	p.logger.Info("Member suspected", "id", memberID)

	// Broadcast suspect message
	suspect := Message{
		Type:        MsgSuspect,
		Source:      p.id,
		Target:      memberID,
		Incarnation: member.Incarnation,
	}
	p.broadcast(suspect)
}

// suspectLoop handles suspect timeouts.
func (p *Protocol) suspectLoop() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-p.stopCh:
			return
		case <-ticker.C:
			p.checkSuspects()
		}
	}
}

// checkSuspects checks for suspects that have timed out.
func (p *Protocol) checkSuspects() {
	p.mu.Lock()
	now := time.Now()
	suspects := make([]string, 0)

	for id, member := range p.members {
		if member.State == StateSuspect && now.Sub(member.LastProbe) > p.suspectTimeout {
			suspects = append(suspects, id)
		}
	}
	p.mu.Unlock()

	for _, id := range suspects {
		p.markDead(id)
	}
}

// markDead marks a member as dead.
func (p *Protocol) markDead(memberID string) {
	p.mu.Lock()
	member, exists := p.members[memberID]
	if !exists {
		p.mu.Unlock()
		return
	}

	member.State = StateDead
	incarnation := member.Incarnation
	p.mu.Unlock()

	p.logger.Info("Member marked dead", "id", memberID)

	// Broadcast dead message
	dead := Message{
		Type:        MsgDead,
		Source:      p.id,
		Target:      memberID,
		Incarnation: incarnation,
	}
	p.broadcast(dead)

	// Send event
	p.eventCh <- Event{
		Type:   EventMemberLeave,
		Member: member,
	}
}

// syncLoop periodically syncs member lists.
func (p *Protocol) syncLoop() {
	ticker := time.NewTicker(p.syncInterval)
	defer ticker.Stop()

	for {
		select {
		case <-p.stopCh:
			return
		case <-ticker.C:
			p.gossip()
		}
	}
}

// gossip gossips member state to a random member.
func (p *Protocol) gossip() {
	member := p.selectRandomMember()
	if member == nil {
		return
	}

	members := p.getMemberInfos()
	msg := Message{
		Type:    MsgSync,
		Source:  p.id,
		Members: members,
	}
	p.sendMessage(member.Address, msg)
}

// broadcast sends a message to all members.
func (p *Protocol) broadcast(msg Message) {
	p.mu.RLock()
	members := make([]*Member, 0, len(p.members))
	for _, m := range p.members {
		if m.State == StateAlive && m.ID != p.id {
			members = append(members, m)
		}
	}
	p.mu.RUnlock()

	for _, m := range members {
		p.sendMessage(m.Address, msg)
	}
}

// selectRandomMember selects a random alive member.
func (p *Protocol) selectRandomMember() *Member {
	p.mu.RLock()
	defer p.mu.RUnlock()

	alive := make([]*Member, 0)
	for _, m := range p.members {
		if m.State == StateAlive && m.ID != p.id {
			alive = append(alive, m)
		}
	}

	if len(alive) == 0 {
		return nil
	}

	return alive[randomInt(len(alive))]
}

// updateMember updates member information.
func (p *Protocol) updateMember(id, addr string, state MemberState, incarnation uint64) {
	// Validate address format to prevent injection via gossip
	if addr != "" && !isValidAddress(addr) {
		p.logger.Debug("Invalid member address", "id", id, "addr", addr)
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	member, exists := p.members[id]
	if !exists {
		// New member
		member = &Member{
			ID:      id,
			Address: addr,
		}
		p.members[id] = member
		p.mu.Unlock()
		p.eventCh <- Event{
			Type:   EventMemberJoin,
			Member: member,
		}
		p.mu.Lock()
	}

	// Only update if incarnation is newer or equal
	if incarnation >= member.Incarnation {
		member.Incarnation = incarnation
		member.State = state
		member.LastSeen = time.Now()
		if addr != "" {
			member.Address = addr
		}
	}
}

// getMemberInfos returns member information for gossip.
func (p *Protocol) getMemberInfos() []MemberInfo {
	p.mu.RLock()
	defer p.mu.RUnlock()

	infos := make([]MemberInfo, 0, len(p.members)+1)

	// Add ourselves
	infos = append(infos, MemberInfo{
		ID:          p.id,
		Address:     p.addr,
		State:       StateAlive,
		Incarnation: p.incarnation(),
	})

	// Add other members
	for _, m := range p.members {
		infos = append(infos, MemberInfo{
			ID:          m.ID,
			Address:     m.Address,
			State:       m.State,
			Incarnation: m.Incarnation,
		})
	}

	return infos
}

// incarnation returns our current incarnation number.
func (p *Protocol) incarnation() uint64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	m, exists := p.members[p.id]
	if !exists {
		return 0
	}
	return m.Incarnation
}

// sendMessage sends a message to an address.
func (p *Protocol) sendMessage(addr string, msg Message) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return err
	}

	_, err = p.listener.WriteTo(data, udpAddr)
	return err
}

// isStopping checks if the protocol is stopping.
func (p *Protocol) isStopping() bool {
	select {
	case <-p.stopCh:
		return true
	default:
		return false
	}
}

// randomInt returns a random int between 0 and n-1.
func randomInt(n int) int {
	if n <= 0 {
		return 0
	}
	return int(time.Now().UnixNano() % int64(n))
}

// isValidAddress checks if an address is a valid host:port.
func isValidAddress(addr string) bool {
	_, _, err := net.SplitHostPort(addr)
	return err == nil
}
