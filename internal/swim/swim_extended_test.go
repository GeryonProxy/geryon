package swim

import (
	"testing"
	"time"

	"github.com/GeryonProxy/geryon/internal/logger"
)

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
	defer p.Stop()

	// Wait for start
	time.Sleep(10 * time.Millisecond)

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
	defer p.Stop()

	// Wait for start
	time.Sleep(10 * time.Millisecond)

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
		ID:        "node-2",
		State:     StateSuspect,
		LastSeen:  time.Now().Add(-time.Hour),
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
	defer p.Stop()

	// Wait for start
	time.Sleep(10 * time.Millisecond)

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
	defer p.Stop()

	// Wait for start
	time.Sleep(10 * time.Millisecond)

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
	defer p.Stop()

	// Wait for start
	time.Sleep(10 * time.Millisecond)

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
	defer p.Stop()

	// Wait for start
	time.Sleep(10 * time.Millisecond)

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
	defer p.Stop()

	// Wait for start
	time.Sleep(10 * time.Millisecond)

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
	defer p.Stop()

	// Wait for start
	time.Sleep(10 * time.Millisecond)

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
		addr    string
		valid   bool
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
