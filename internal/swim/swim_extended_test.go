package swim

import (
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/GeryonProxy/geryon/internal/logger"
)

// waitForCondition polls the given check function until it returns true or the
// timeout expires. Returns true if the condition was met.
func waitForCondition(t *testing.T, timeout time.Duration, check func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if check() {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

func TestProtocol_MemberCount(t *testing.T) {
	log, _ := logger.New("debug", "text")
	p := NewProtocol("node-1", "127.0.0.1:0", log)

	// Initially 0
	if p.MemberCount() != 0 {
		t.Errorf("MemberCount() = %d, want 0", p.MemberCount())
	}

	// Add some members
	p.members["node-2"] = &Member{ID: "node-2", State: StateAlive}
	p.members["node-3"] = &Member{ID: "node-3", State: StateSuspect}

	if p.MemberCount() != 2 {
		t.Errorf("MemberCount() = %d, want 2", p.MemberCount())
	}
}

func TestProtocol_updateMember(t *testing.T) {
	log, _ := logger.New("debug", "text")
	p := NewProtocol("node-1", "127.0.0.1:0", log)

	// Create event channel to capture join event
	evCh := p.Events()

	// Update with new member
	p.updateMember("node-2", "127.0.0.1:7946", StateAlive, 1)

	// Should have member
	if _, exists := p.members["node-2"]; !exists {
		t.Error("Member should exist after update")
	}

	// Should receive join event
	select {
	case ev := <-evCh:
		if ev.Type != EventMemberJoin {
			t.Errorf("Event type = %v, want EventMemberJoin", ev.Type)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("Should have received join event")
	}

	// Update with same incarnation - should update
	p.updateMember("node-2", "127.0.0.1:7947", StateAlive, 1)
	if p.members["node-2"].Address != "127.0.0.1:7947" {
		t.Error("Address should be updated")
	}

	// Update with lower incarnation - should not update
	oldAddr := p.members["node-2"].Address
	p.updateMember("node-2", "127.0.0.1:7948", StateAlive, 0)
	if p.members["node-2"].Address != oldAddr {
		t.Error("Address should not be updated with lower incarnation")
	}
}

func TestProtocol_updateMember_InvalidAddress(t *testing.T) {
	log, _ := logger.New("debug", "text")
	p := NewProtocol("node-1", "127.0.0.1:0", log)

	// Update with invalid address - should be ignored
	p.updateMember("node-2", "invalid_address", StateAlive, 1)

	// Member should not exist
	if _, exists := p.members["node-2"]; exists {
		t.Error("Member should not be added with invalid address")
	}
}

func TestProtocol_getMemberInfos(t *testing.T) {
	log, _ := logger.New("debug", "text")
	p := NewProtocol("node-1", "127.0.0.1:7946", log)

	// Add some members
	p.members["node-2"] = &Member{
		ID:          "node-2",
		Address:     "127.0.0.1:7947",
		State:       StateAlive,
		Incarnation: 5,
	}

	infos := p.getMemberInfos()

	// Should include ourselves + members
	if len(infos) != 2 {
		t.Errorf("len(infos) = %d, want 2", len(infos))
	}

	// Check that we have our own info
	foundSelf := false
	for _, info := range infos {
		if info.ID == "node-1" {
			foundSelf = true
			if info.State != StateAlive {
				t.Error("Self should be alive")
			}
		}
	}
	if !foundSelf {
		t.Error("Should include self in member infos")
	}
}

func TestProtocol_incarnation(t *testing.T) {
	log, _ := logger.New("debug", "text")
	p := NewProtocol("node-1", "127.0.0.1:0", log)

	// Initially 0
	if p.incarnation() != 0 {
		t.Errorf("incarnation() = %d, want 0", p.incarnation())
	}

	// After adding ourselves
	p.members["node-1"] = &Member{
		ID:          "node-1",
		Incarnation: 10,
	}
	if p.incarnation() != 10 {
		t.Errorf("incarnation() = %d, want 10", p.incarnation())
	}
}

func TestProtocol_isStopping(t *testing.T) {
	log, _ := logger.New("debug", "text")
	p := NewProtocol("node-1", "127.0.0.1:0", log)

	if p.isStopping() {
		t.Error("isStopping should be false initially")
	}

	close(p.stopCh)
	if !p.isStopping() {
		t.Error("isStopping should be true after stopCh closed")
	}
}

func TestProtocol_selectRandomMember(t *testing.T) {
	log, _ := logger.New("debug", "text")
	p := NewProtocol("node-1", "127.0.0.1:0", log)

	// Initially nil (no other members)
	if p.selectRandomMember() != nil {
		t.Error("selectRandomMember should return nil with no members")
	}

	// Add some members
	p.members["node-2"] = &Member{ID: "node-2", State: StateAlive}
	p.members["node-3"] = &Member{ID: "node-3", State: StateAlive}

	// Should return one of them
	for i := 0; i < 10; i++ {
		m := p.selectRandomMember()
		if m == nil {
			t.Error("selectRandomMember should not return nil")
			continue
		}
		if m.ID != "node-2" && m.ID != "node-3" {
			t.Errorf("Unexpected member ID: %s", m.ID)
		}
		if m.ID == "node-1" {
			t.Error("Should not select self")
		}
	}

	// Only dead members
	p.members["node-2"].State = StateDead
	p.members["node-3"].State = StateDead
	if p.selectRandomMember() != nil {
		t.Error("selectRandomMember should return nil with only dead members")
	}
}

func TestProtocol_suspectMember(t *testing.T) {
	log, _ := logger.New("debug", "text")
	p := NewProtocol("node-1", "127.0.0.1:0", log)

	// Add a member
	p.members["node-2"] = &Member{ID: "node-2", State: StateAlive}

	// Suspect it
	p.suspectMember("node-2")

	if p.members["node-2"].State != StateSuspect {
		t.Errorf("State = %v, want Suspect", p.members["node-2"].State)
	}

	// Suspect non-existent member
	p.suspectMember("node-99") // Should not panic

	// Suspect already suspect member
	p.suspectMember("node-2") // Should not panic
}

func TestProtocol_markDead(t *testing.T) {
	log, _ := logger.New("debug", "text")
	p := NewProtocol("node-1", "127.0.0.1:0", log)

	// Create event channel
	evCh := p.Events()

	// Add a member
	p.members["node-2"] = &Member{ID: "node-2", State: StateSuspect, Incarnation: 5}

	// Mark dead
	p.markDead("node-2")

	if p.members["node-2"].State != StateDead {
		t.Errorf("State = %v, want Dead", p.members["node-2"].State)
	}

	// Should receive leave event
	select {
	case ev := <-evCh:
		if ev.Type != EventMemberLeave {
			t.Errorf("Event type = %v, want EventMemberLeave", ev.Type)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("Should have received leave event")
	}

	// Mark non-existent dead
	p.markDead("node-99") // Should not panic
}

func TestProtocol_checkSuspects(t *testing.T) {
	log, _ := logger.New("debug", "text")
	p := NewProtocol("node-1", "127.0.0.1:0", log)

	// Add a suspect member with old LastProbe
	p.members["node-2"] = &Member{
		ID:        "node-2",
		State:     StateSuspect,
		LastProbe: time.Now().Add(-10 * time.Second), // Very old
	}

	p.suspectTimeout = 1 * time.Second // Short timeout

	p.checkSuspects()

	if p.members["node-2"].State != StateDead {
		t.Errorf("State = %v, want Dead", p.members["node-2"].State)
	}
}

func TestProtocol_handleMessage(t *testing.T) {
	log, _ := logger.New("debug", "text")
	p := NewProtocol("node-1", "127.0.0.1:0", log)
	p.Start()
	p.WaitReady()
	defer p.Stop()

	// Wait for start

	// Test all message types - should not panic
	p.handleMessage(Message{Type: MsgPing, Source: "node-2"}, "127.0.0.1:7946")
	p.handleMessage(Message{Type: MsgAck, Source: "node-2"}, "127.0.0.1:7946")
	p.handleMessage(Message{Type: MsgPingReq, Source: "node-2"}, "127.0.0.1:7946")
	p.handleMessage(Message{Type: MsgSuspect, Source: "node-2", Target: "node-3"}, "127.0.0.1:7946")
	p.handleMessage(Message{Type: MsgAlive, Source: "node-2"}, "127.0.0.1:7946")
	p.handleMessage(Message{Type: MsgDead, Source: "node-2", Target: "node-3"}, "127.0.0.1:7946")
	p.handleMessage(Message{Type: MsgSync, Source: "node-2"}, "127.0.0.1:7946")
	p.handleMessage(Message{Type: 99}, "127.0.0.1:7946") // Unknown type
}

func TestProtocol_handlePing(t *testing.T) {
	log, _ := logger.New("debug", "text")
	p := NewProtocol("node-1", "127.0.0.1:0", log)
	p.Start()
	p.WaitReady()
	defer p.Stop()

	// Wait for start

	msg := Message{
		Type:        MsgPing,
		Source:      "node-2",
		Incarnation: 1,
	}

	p.handlePing(msg, "127.0.0.1:7946")

	// Should have updated member
	if _, exists := p.members["node-2"]; !exists {
		t.Error("Member should exist after ping")
	}
}

func TestProtocol_handleAck(t *testing.T) {
	log, _ := logger.New("debug", "text")
	p := NewProtocol("node-1", "127.0.0.1:0", log)

	// Add a member
	p.members["node-2"] = &Member{
		ID:       "node-2",
		State:    StateSuspect,
		LastSeen: time.Now().Add(-time.Hour),
	}

	msg := Message{
		Type:   MsgAck,
		Source: "node-2",
	}

	p.handleAck(msg)

	if p.members["node-2"].State != StateAlive {
		t.Error("State should be Alive after ack")
	}
}

func TestProtocol_handleSuspect(t *testing.T) {
	log, _ := logger.New("debug", "text")
	p := NewProtocol("node-1", "127.0.0.1:0", log)

	// Add a member
	p.members["node-2"] = &Member{
		ID:          "node-2",
		State:       StateAlive,
		Incarnation: 1,
	}

	msg := Message{
		Type:        MsgSuspect,
		Source:      "node-3",
		Target:      "node-2",
		Incarnation: 1,
	}

	p.handleSuspect(msg)

	if p.members["node-2"].State != StateSuspect {
		t.Error("State should be Suspect")
	}

	// Suspect with lower incarnation - should not change
	p.members["node-2"].State = StateAlive
	p.members["node-2"].Incarnation = 5
	msg.Incarnation = 1
	p.handleSuspect(msg)

	if p.members["node-2"].State != StateAlive {
		t.Error("State should remain Alive with lower incarnation")
	}
}

func TestProtocol_handleDead(t *testing.T) {
	log, _ := logger.New("debug", "text")
	p := NewProtocol("node-1", "127.0.0.1:0", log)

	// Add a member
	p.members["node-2"] = &Member{
		ID:          "node-2",
		State:       StateSuspect,
		Incarnation: 1,
	}

	msg := Message{
		Type:        MsgDead,
		Source:      "node-3",
		Target:      "node-2",
		Incarnation: 1,
	}

	p.handleDead(msg)

	if p.members["node-2"].State != StateDead {
		t.Error("State should be Dead")
	}
}

func TestProtocol_handleAlive(t *testing.T) {
	log, _ := logger.New("debug", "text")
	p := NewProtocol("node-1", "127.0.0.1:0", log)

	msg := Message{
		Type:        MsgAlive,
		Source:      "node-2",
		Incarnation: 1,
		Payload:     []byte(`{"address":"127.0.0.1:7947"}`),
	}

	p.handleAlive(msg)

	if _, exists := p.members["node-2"]; !exists {
		t.Error("Member should exist after alive message")
	}

	// Test with invalid payload
	msg.Payload = []byte(`invalid json`)
	p.handleAlive(msg) // Should not panic
}

func TestProtocol_handleSync(t *testing.T) {
	log, _ := logger.New("debug", "text")
	p := NewProtocol("node-1", "127.0.0.1:0", log)
	p.Start()
	p.WaitReady()
	defer p.Stop()

	// Wait for start

	msg := Message{
		Type:    MsgSync,
		Source:  "node-2",
		Payload: []byte("127.0.0.1:7947"),
	}

	p.handleSync(msg)

	if _, exists := p.members["node-2"]; !exists {
		t.Error("Member should exist after sync")
	}
}

func TestProtocol_SendTo(t *testing.T) {
	log, _ := logger.New("debug", "text")
	p := NewProtocol("node-1", "127.0.0.1:0", log)
	p.Start()
	p.WaitReady()
	defer p.Stop()

	// Wait for start

	// Send to invalid address - should error
	err := p.SendTo("invalid_address", []byte("test"))
	if err == nil {
		t.Error("SendTo should fail for invalid address")
	}
}

func TestProtocol_BroadcastUserData(t *testing.T) {
	log, _ := logger.New("debug", "text")
	p := NewProtocol("node-1", "127.0.0.1:0", log)
	p.Start()
	p.WaitReady()
	defer p.Stop()

	// Wait for start

	// Broadcast with no members - should not panic
	p.BroadcastUserData([]byte("test data"))

	// Add members
	p.members["node-2"] = &Member{ID: "node-2", Address: "127.0.0.1:7947", State: StateAlive}

	// Broadcast again - should not panic
	p.BroadcastUserData([]byte("test data"))
}

func TestProtocol_probeRandomMember(t *testing.T) {
	log, _ := logger.New("debug", "text")
	p := NewProtocol("node-1", "127.0.0.1:0", log)
	p.Start()
	p.WaitReady()
	defer p.Stop()

	// Wait for start

	// With no members - should not panic
	p.probeRandomMember()

	// Add self - should not probe self
	p.members["node-1"] = &Member{ID: "node-1", Address: "127.0.0.1:7946", State: StateAlive}
	p.probeRandomMember()
}

func TestProtocol_gossip(t *testing.T) {
	log, _ := logger.New("debug", "text")
	p := NewProtocol("node-1", "127.0.0.1:0", log)
	p.Start()
	p.WaitReady()
	defer p.Stop()

	// Wait for start

	// With no members - should not panic
	p.gossip()

	// Add members
	p.members["node-2"] = &Member{ID: "node-2", Address: "127.0.0.1:7947", State: StateAlive}
	p.gossip() // Should not panic
}

func TestProtocol_broadcast(t *testing.T) {
	log, _ := logger.New("debug", "text")
	p := NewProtocol("node-1", "127.0.0.1:0", log)
	p.Start()
	p.WaitReady()
	defer p.Stop()

	// Wait for start

	msg := Message{Type: MsgPing, Source: "node-1"}

	// With no members - should not panic
	p.broadcast(msg)

	// Add members
	p.members["node-2"] = &Member{ID: "node-2", Address: "127.0.0.1:7947", State: StateAlive}
	p.members["node-3"] = &Member{ID: "node-3", Address: "127.0.0.1:7948", State: StateDead}

	p.broadcast(msg) // Should broadcast to alive member only
}

func TestIsValidAddress(t *testing.T) {
	tests := []struct {
		addr  string
		valid bool
	}{
		{"127.0.0.1:7946", true},
		{"192.168.1.1:8080", true},
		{"localhost:3000", true},
		{"invalid", false},
		{"", false},
		{"127.0.0.1", false}, // Missing port
	}

	for _, tt := range tests {
		result := isValidAddress(tt.addr)
		if result != tt.valid {
			t.Errorf("isValidAddress(%q) = %v, want %v", tt.addr, result, tt.valid)
		}
	}
}

func TestRandomInt_Extended(t *testing.T) {
	// Test with n=0
	if randomInt(0) != 0 {
		t.Error("randomInt(0) should return 0")
	}

	// Test with negative
	if randomInt(-1) != 0 {
		t.Error("randomInt(-1) should return 0")
	}

	// Test normal case
	for i := 0; i < 100; i++ {
		n := randomInt(10)
		if n < 0 || n >= 10 {
			t.Errorf("randomInt(10) = %d, want [0,10)", n)
		}
	}
}

// --- Tests for improved coverage on low-coverage functions ---

func TestProtocol_receiveLoop_ValidMessage(t *testing.T) {
	log, _ := logger.New("debug", "text")
	p1 := NewProtocol("node-1", "127.0.0.1:0", log)
	if err := p1.Start(); err != nil {
		t.Fatalf("Start p1 failed: %v", err)
	}
	defer p1.Stop()
	p1.WaitReady()

	// Get p1's actual address from its listener
	p1Addr := p1.listener.LocalAddr().String()

	// Create a second protocol to send a sync message to p1
	log2, _ := logger.New("debug", "text")
	p2 := NewProtocol("node-2", "127.0.0.1:0", log2)
	if err := p2.Start(); err != nil {
		t.Fatalf("Start p2 failed: %v", err)
	}
	defer p2.Stop()
	p2.WaitReady()

	// Send a sync message from p2 to p1
	msg := Message{
		Type:    MsgSync,
		Source:  "node-2",
		Payload: []byte("127.0.0.1:0"),
	}

	// Use p2's sendMessage to send to p1's address
	if err := p2.sendMessage(p1Addr, msg); err != nil {
		t.Fatalf("Failed to send message: %v", err)
	}

	// Wait for p1 to process the message
	if !waitForCondition(t, 5*time.Second, func() bool {
		p1.mu.RLock()
		_, exists := p1.members["node-2"]
		p1.mu.RUnlock()
		return exists
	}) {
		t.Fatal("node-2 should exist in p1's member list after receiving sync")
	}

	// Verify p1 received the sync and added node-2
}

func TestProtocol_receiveLoop_InvalidJSON(t *testing.T) {
	log, _ := logger.New("debug", "text")
	p := NewProtocol("node-1", "127.0.0.1:0", log)
	if err := p.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer p.Stop()
	p.WaitReady()

	addr := p.listener.LocalAddr().String()

	// Create a raw UDP connection and send invalid JSON
	conn, err := net.Dial("udp", addr)
	if err != nil {
		t.Fatalf("Failed to dial: %v", err)
	}
	defer conn.Close()

	// Send invalid JSON - receiveLoop should log and continue, not crash
	_, err = conn.Write([]byte("not valid json"))
	if err != nil {
		t.Fatalf("Failed to write: %v", err)
	}

	// Wait a bit to ensure receiveLoop processes it
	time.Sleep(50 * time.Millisecond)

	// Protocol should still be running and functional
	if p.isStopping() {
		t.Error("Protocol should still be running after invalid JSON")
	}
}

func TestProtocol_probeRandomMember_WithMember(t *testing.T) {
	log, _ := logger.New("debug", "text")
	p1 := NewProtocol("node-1", "127.0.0.1:0", log)
	if err := p1.Start(); err != nil {
		t.Fatalf("Start p1 failed: %v", err)
	}
	defer p1.Stop()
	p1.WaitReady()

	_ = p1.listener.LocalAddr().String()

	// Create a second protocol to respond to pings
	log2, _ := logger.New("debug", "text")
	p2 := NewProtocol("node-2", "127.0.0.1:0", log2)
	if err := p2.Start(); err != nil {
		t.Fatalf("Start p2 failed: %v", err)
	}
	defer p2.Stop()
	p2.WaitReady()

	p2Addr := p2.listener.LocalAddr().String()

	// Manually add p2 as a member of p1 with the real address
	p1.mu.Lock()
	p1.members["node-2"] = &Member{
		ID:      "node-2",
		Address: p2Addr,
		State:   StateAlive,
	}
	p1.mu.Unlock()

	// Probe should send a ping and eventually get an ack
	p1.probeRandomMember()

	// Wait for the ack to be processed
	if !waitForCondition(t, 5*time.Second, func() bool {
		p1.mu.RLock()
		defer p1.mu.RUnlock()
		m := p1.members["node-2"]
		return m != nil && m.State == StateAlive
	}) {
		t.Fatal("node-2 should be alive after successful probe")
	}

	// The member should still be alive (got ack)
	p1.mu.RLock()
	member := p1.members["node-2"]
	p1.mu.RUnlock()

	if member == nil {
		t.Fatal("node-2 should still be in member list")
	}
	if member.State == StateDead {
		t.Error("node-2 should not be dead after successful probe")
	}
}

func TestProtocol_probeRandomMember_Timeout(t *testing.T) {
	log, _ := logger.New("debug", "text")
	p := NewProtocol("node-1", "127.0.0.1:0", log)
	if err := p.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer p.Stop()
	p.WaitReady()

	// Set a very short probe timeout for testing
	p.probeTimeout = 10 * time.Millisecond

	// Add a member pointing to a non-existent address so it won't respond
	p.mu.Lock()
	p.members["node-2"] = &Member{
		ID:      "node-2",
		Address: "127.0.0.1:1", // port 1 - unlikely to have a listener
		State:   StateAlive,
	}
	p.mu.Unlock()

	// Probe the member - it won't respond
	p.probeRandomMember()

	// Wait for probe timeout to trigger suspect
	if !waitForCondition(t, 5*time.Second, func() bool {
		p.mu.RLock()
		defer p.mu.RUnlock()
		m := p.members["node-2"]
		return m != nil && m.State == StateSuspect
	}) {
		t.Fatal("node-2 should be suspected after probe timeout")
	}

	p.mu.RLock()
	member := p.members["node-2"]
	p.mu.RUnlock()

	if member == nil {
		t.Fatal("node-2 should still be in member list")
	}
	if member.State != StateSuspect {
		t.Errorf("node-2 should be suspect after probe timeout, got state %v", member.State)
	}
}

func TestProtocol_handlePingReq_ValidPayload(t *testing.T) {
	log, _ := logger.New("debug", "text")
	p1 := NewProtocol("node-1", "127.0.0.1:0", log)
	if err := p1.Start(); err != nil {
		t.Fatalf("Start p1 failed: %v", err)
	}
	defer p1.Stop()
	p1.WaitReady()

	_ = p1.listener.LocalAddr().String()

	// Create a target node
	log2, _ := logger.New("debug", "text")
	p2 := NewProtocol("node-2", "127.0.0.1:0", log2)
	if err := p2.Start(); err != nil {
		t.Fatalf("Start p2 failed: %v", err)
	}
	defer p2.Stop()
	p2.WaitReady()

	p2Addr := p2.listener.LocalAddr().String()

	// Create a PingReq message with valid payload targeting node-2
	payload, _ := json.Marshal(struct {
		Target string `json:"target"`
	}{Target: p2Addr})

	msg := Message{
		Type:    MsgPingReq,
		Source:  "node-3",
		Payload: payload,
	}

	// handlePingReq should forward a ping to the target
	p1.handlePingReq(msg)

	// Wait for p2 to receive the forwarded ping
	time.Sleep(500 * time.Millisecond)

	// node-3 should have been added to p2 via the forwarded ping (or at least p1 sent the message)
	// The key thing is no panic occurred
}

func TestProtocol_handlePingReq_InvalidPayload(t *testing.T) {
	log, _ := logger.New("debug", "text")
	p := NewProtocol("node-1", "127.0.0.1:0", log)

	msg := Message{
		Type:    MsgPingReq,
		Source:  "node-2",
		Payload: []byte("not json"),
	}

	// Should not panic
	p.handlePingReq(msg)
}

func TestProtocol_Join_SelfSkip(t *testing.T) {
	log, _ := logger.New("debug", "text")
	p := NewProtocol("node-1", "127.0.0.1:7946", log)
	if err := p.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer p.Stop()
	p.WaitReady()

	// Join with self address should skip and return error (no valid seeds)
	err := p.Join([]string{"127.0.0.1:7946"})
	if err == nil {
		t.Error("Join with only self should return error")
	}
}

func TestProtocol_Join_AllSeedsFail(t *testing.T) {
	log, _ := logger.New("debug", "text")
	p := NewProtocol("node-1", "127.0.0.1:0", log)
	if err := p.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer p.Stop()
	p.WaitReady()

	// Join with invalid addresses that will fail DNS resolution
	err := p.Join([]string{"invalid-host-that-does-not-exist:7946"})
	if err == nil {
		t.Error("Join with invalid seed should return error")
	}
}

func TestProtocol_Join_ValidSeed(t *testing.T) {
	log, _ := logger.New("debug", "text")
	p1 := NewProtocol("node-1", "127.0.0.1:0", log)
	if err := p1.Start(); err != nil {
		t.Fatalf("Start p1 failed: %v", err)
	}
	defer p1.Stop()
	p1.WaitReady()

	p1Addr := p1.listener.LocalAddr().String()

	// Create p2 and have it join p1
	log2, _ := logger.New("debug", "text")
	p2 := NewProtocol("node-2", "127.0.0.1:0", log2)
	if err := p2.Start(); err != nil {
		t.Fatalf("Start p2 failed: %v", err)
	}
	defer p2.Stop()
	p2.WaitReady()

	err := p2.Join([]string{p1Addr})
	if err != nil {
		t.Errorf("Join should succeed with valid seed: %v", err)
	}

	// Wait for p1 to learn about node-2
	if !waitForCondition(t, 5*time.Second, func() bool {
		p1.mu.RLock()
		defer p1.mu.RUnlock()
		_, exists := p1.members["node-2"]
		return exists
	}) {
		t.Fatal("p1 should have node-2 in its member list")
	}
}

func TestProtocol_Start_InvalidAddress(t *testing.T) {
	log, _ := logger.New("debug", "text")
	p := NewProtocol("node-1", "not-a-valid-address:xxx", log)

	err := p.Start()
	if err == nil {
		p.Stop()
		t.Error("Start should fail with invalid address")
	}
}

func TestProtocol_Members_MixedStates(t *testing.T) {
	log, _ := logger.New("debug", "text")
	p := NewProtocol("node-1", "127.0.0.1:0", log)

	// Add members in various states
	p.mu.Lock()
	p.members["node-2"] = &Member{ID: "node-2", State: StateAlive}
	p.members["node-3"] = &Member{ID: "node-3", State: StateSuspect}
	p.members["node-4"] = &Member{ID: "node-4", State: StateDead}
	p.members["node-5"] = &Member{ID: "node-5", State: StateAlive}
	p.mu.Unlock()

	members := p.Members()

	// Should only return alive members
	if len(members) != 2 {
		t.Errorf("Members() returned %d members, want 2", len(members))
	}

	for _, m := range members {
		if m.State != StateAlive {
			t.Errorf("Members() returned member %s in state %v, want Alive", m.ID, m.State)
		}
	}
}

func TestProtocol_receiveLoop_TwoNodeCluster(t *testing.T) {
	log1, _ := logger.New("debug", "text")
	p1 := NewProtocol("node-1", "127.0.0.1:0", log1)
	if err := p1.Start(); err != nil {
		t.Fatalf("Start p1 failed: %v", err)
	}
	defer p1.Stop()

	log2, _ := logger.New("debug", "text")
	p2 := NewProtocol("node-2", "127.0.0.1:0", log2)
	if err := p2.Start(); err != nil {
		t.Fatalf("Start p2 failed: %v", err)
	}
	defer p2.Stop()
	p2.WaitReady()

	_ = p1.listener.LocalAddr().String()
	p2Addr := p2.listener.LocalAddr().String()

	// Send a ping from p1 to p2
	pingMsg := Message{
		Type:   MsgPing,
		Source: "node-1",
	}
	if err := p1.sendMessage(p2Addr, pingMsg); err != nil {
		t.Fatalf("Failed to send ping: %v", err)
	}

	// Wait for p2 to process the ping and add node-1
	if !waitForCondition(t, 5*time.Second, func() bool {
		p2.mu.RLock()
		defer p2.mu.RUnlock()
		_, exists := p2.members["node-1"]
		return exists
	}) {
		t.Fatal("p2 should have node-1 as a member")
	}

	// p2 should have node-1 as a member
	p2.mu.RLock()
	m, exists := p2.members["node-1"]
	p2.mu.RUnlock()

	if !exists {
		t.Error("p2 should have node-1 as a member after receiving ping")
	} else if m.State != StateAlive {
		t.Errorf("node-1 state = %v, want Alive", m.State)
	}
}

func TestProtocol_sendMessage_NoListener(t *testing.T) {
	log, _ := logger.New("debug", "text")
	p := NewProtocol("node-1", "127.0.0.1:0", log)
	// Not started, so listener is nil - sendMessage will panic

	msg := Message{Type: MsgPing, Source: "node-1"}
	defer func() {
		if r := recover(); r == nil {
			t.Error("sendMessage should panic with nil listener")
		}
	}()
	p.sendMessage("127.0.0.1:7946", msg)
}

func TestProtocol_sendMessage_InvalidAddr(t *testing.T) {
	log, _ := logger.New("debug", "text")
	p := NewProtocol("node-1", "127.0.0.1:0", log)
	if err := p.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer p.Stop()
	p.WaitReady()

	msg := Message{Type: MsgPing, Source: "node-1"}
	err := p.sendMessage("invalid-addr", msg)
	if err == nil {
		t.Error("sendMessage should fail with invalid address")
	}
}

func TestProtocol_checkSuspects_NoTimeout(t *testing.T) {
	log, _ := logger.New("debug", "text")
	p := NewProtocol("node-1", "127.0.0.1:0", log)

	// Add a suspect member with recent LastProbe (should NOT time out)
	p.mu.Lock()
	p.members["node-2"] = &Member{
		ID:        "node-2",
		State:     StateSuspect,
		LastProbe: time.Now(), // Just probed
	}
	p.suspectTimeout = 1 * time.Hour // Very long timeout
	p.mu.Unlock()

	p.checkSuspects()

	p.mu.RLock()
	state := p.members["node-2"].State
	p.mu.RUnlock()

	if state != StateSuspect {
		t.Errorf("State = %v, want Suspect (should not time out yet)", state)
	}
}

func TestProtocol_suspectMember_AlreadySuspect(t *testing.T) {
	log, _ := logger.New("debug", "text")
	p := NewProtocol("node-1", "127.0.0.1:0", log)

	p.mu.Lock()
	p.members["node-2"] = &Member{ID: "node-2", State: StateSuspect}
	p.mu.Unlock()

	// Suspect an already-suspect member - should return early, no broadcast
	p.suspectMember("node-2")

	p.mu.RLock()
	state := p.members["node-2"].State
	p.mu.RUnlock()

	if state != StateSuspect {
		t.Errorf("State = %v, want Suspect (should remain unchanged)", state)
	}
}

func TestProtocol_markDead_AlreadyDead(t *testing.T) {
	log, _ := logger.New("debug", "text")
	p := NewProtocol("node-1", "127.0.0.1:0", log)

	evCh := p.Events()

	p.mu.Lock()
	p.members["node-2"] = &Member{ID: "node-2", State: StateDead, Incarnation: 1}
	p.mu.Unlock()

	// Mark already-dead member - should still fire event
	p.markDead("node-2")

	select {
	case ev := <-evCh:
		if ev.Type != EventMemberLeave {
			t.Errorf("Event type = %v, want EventMemberLeave", ev.Type)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("Should have received leave event even for already-dead member")
	}
}

func TestProtocol_handleDead_HigherIncarnation(t *testing.T) {
	log, _ := logger.New("debug", "text")
	p := NewProtocol("node-1", "127.0.0.1:0", log)

	p.mu.Lock()
	p.members["node-2"] = &Member{ID: "node-2", State: StateAlive, Incarnation: 5}
	p.mu.Unlock()

	// Dead message with lower incarnation - should NOT mark dead
	msg := Message{
		Type:        MsgDead,
		Source:      "node-3",
		Target:      "node-2",
		Incarnation: 3,
	}
	p.handleDead(msg)

	p.mu.RLock()
	state := p.members["node-2"].State
	p.mu.RUnlock()

	if state != StateAlive {
		t.Errorf("State = %v, want Alive (should not mark dead with lower incarnation)", state)
	}
}

func TestProtocol_handleDead_NonExistent(t *testing.T) {
	log, _ := logger.New("debug", "text")
	p := NewProtocol("node-1", "127.0.0.1:0", log)

	// Should not panic with non-existent target
	msg := Message{
		Type:        MsgDead,
		Source:      "node-3",
		Target:      "node-99",
		Incarnation: 1,
	}
	p.handleDead(msg)
}

func TestProtocol_updateMember_EmptyAddr(t *testing.T) {
	log, _ := logger.New("debug", "text")
	p := NewProtocol("node-1", "127.0.0.1:0", log)

	evCh := p.Events()

	// Update with empty address - should still add the member (empty addr is allowed)
	p.updateMember("node-2", "", StateAlive, 1)

	if _, exists := p.members["node-2"]; !exists {
		t.Error("Member should be added even with empty address")
	}

	// Should receive join event
	select {
	case ev := <-evCh:
		if ev.Type != EventMemberJoin {
			t.Errorf("Event type = %v, want EventMemberJoin", ev.Type)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("Should have received join event")
	}
}

func TestProtocol_gossip_WithMembers(t *testing.T) {
	log, _ := logger.New("debug", "text")
	p1 := NewProtocol("node-1", "127.0.0.1:0", log)
	if err := p1.Start(); err != nil {
		t.Fatalf("Start p1 failed: %v", err)
	}
	defer p1.Stop()

	log2, _ := logger.New("debug", "text")
	p2 := NewProtocol("node-2", "127.0.0.1:0", log2)
	if err := p2.Start(); err != nil {
		t.Fatalf("Start p2 failed: %v", err)
	}
	defer p2.Stop()
	p2.WaitReady()

	p2Addr := p2.listener.LocalAddr().String()

	// Add p2 as member of p1 with real address
	p1.mu.Lock()
	p1.members["node-2"] = &Member{
		ID:      "node-2",
		Address: p2Addr,
		State:   StateAlive,
	}
	p1.mu.Unlock()

	// Gossip should send member list to p2
	p1.gossip()

	if !waitForCondition(t, 5*time.Second, func() bool {
		p2.mu.RLock()
		defer p2.mu.RUnlock()
		_, exists := p2.members["node-1"]
		return exists
	}) {
		t.Fatal("p2 should have node-1 in its member list after gossip")
	}
}
