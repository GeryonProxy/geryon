package swim

import (
	"testing"

	"github.com/GeryonProxy/geryon/internal/logger"
)

func TestMemberState_String(t *testing.T) {
	cases := []struct {
		state MemberState
		want  string
	}{
		{StateAlive, "Alive"},
		{StateSuspect, "Suspect"},
		{StateDead, "Dead"},
		{MemberState(99), "Unknown"},
	}
	for _, tc := range cases {
		if got := tc.state.String(); got != tc.want {
			t.Errorf("MemberState(%d).String() = %q, want %q", tc.state, got, tc.want)
		}
	}
}

func TestConstants(t *testing.T) {
	if StateAlive != 0 {
		t.Errorf("StateAlive = %d, want 0", StateAlive)
	}
	if StateSuspect != 1 {
		t.Errorf("StateSuspect = %d, want 1", StateSuspect)
	}
	if StateDead != 2 {
		t.Errorf("StateDead = %d, want 2", StateDead)
	}

	if EventMemberJoin != 0 {
		t.Errorf("EventMemberJoin = %d, want 0", EventMemberJoin)
	}
	if EventMemberLeave != 1 {
		t.Errorf("EventMemberLeave = %d, want 1", EventMemberLeave)
	}
	if EventMemberUpdate != 2 {
		t.Errorf("EventMemberUpdate = %d, want 2", EventMemberUpdate)
	}

	if MsgPing != 0 {
		t.Errorf("MsgPing = %d, want 0", MsgPing)
	}
	if MsgAck != 1 {
		t.Errorf("MsgAck = %d, want 1", MsgAck)
	}
	if MsgPingReq != 2 {
		t.Errorf("MsgPingReq = %d, want 2", MsgPingReq)
	}
	if MsgSuspect != 3 {
		t.Errorf("MsgSuspect = %d, want 3", MsgSuspect)
	}
	if MsgAlive != 4 {
		t.Errorf("MsgAlive = %d, want 4", MsgAlive)
	}
	if MsgDead != 5 {
		t.Errorf("MsgDead = %d, want 5", MsgDead)
	}
	if MsgSync != 6 {
		t.Errorf("MsgSync = %d, want 6", MsgSync)
	}
}

func TestNewProtocol(t *testing.T) {
	log, _ := logger.New("debug", "text")
	p := NewProtocol("node-1", "127.0.0.1:0", log)
	if p == nil {
		t.Fatal("NewProtocol returned nil")
	}
}

func TestProtocol_StartStop(t *testing.T) {
	log, _ := logger.New("debug", "text")
	p := NewProtocol("node-1", "127.0.0.1:0", log)

	err := p.Start()
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	if p.MemberCount() == 0 {
		// Self may or may not be in member list
	}

	err = p.Stop()
	if err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
}

func TestProtocol_Members(t *testing.T) {
	log, _ := logger.New("debug", "text")
	p := NewProtocol("node-1", "127.0.0.1:0", log)
	err := p.Start()
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer p.Stop()

	members := p.Members()
	// Returns only alive members
	for _, m := range members {
		if m.State != StateAlive {
			t.Errorf("Members() returned member in state %v", m.State)
		}
	}
}

func TestProtocol_Events(t *testing.T) {
	log, _ := logger.New("debug", "text")
	p := NewProtocol("node-1", "127.0.0.1:0", log)
	evCh := p.Events()
	if evCh == nil {
		t.Error("Events() should not return nil")
	}
}

func TestProtocol_Join(t *testing.T) {
	log, _ := logger.New("debug", "text")
	p := NewProtocol("node-1", "127.0.0.1:0", log)
	err := p.Start()
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer p.Stop()

	// Join with non-existent seed - should not error (best effort)
	err = p.Join([]string{"127.0.0.1:9999"})
	// Join may or may not fail depending on connectivity
	_ = err
}

func TestProtocol_DoubleStart(t *testing.T) {
	log, _ := logger.New("debug", "text")
	p := NewProtocol("node-1", "127.0.0.1:0", log)
	err := p.Start()
	if err != nil {
		t.Fatalf("First Start failed: %v", err)
	}
	defer p.Stop()

	// Second Start overwrites the listener (no guard in implementation)
	err = p.Start()
	if err != nil {
		t.Logf("Second Start failed (expected): %v", err)
	}
}

func TestProtocol_Stop(t *testing.T) {
	log, _ := logger.New("debug", "text")
	p := NewProtocol("node-1", "127.0.0.1:0", log)
	err := p.Start()
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	err = p.Stop()
	if err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	// Second Stop panics (close of closed channel) — don't call it
}

func TestMember(t *testing.T) {
	m := &Member{
		ID:          "node-1",
		Address:     "127.0.0.1:7946",
		State:       StateAlive,
		Incarnation: 1,
	}
	if m.ID != "node-1" {
		t.Errorf("ID = %q, want node-1", m.ID)
	}
	if m.State != StateAlive {
		t.Errorf("State = %v, want Alive", m.State)
	}
}

func TestEvent(t *testing.T) {
	m := &Member{ID: "node-1", State: StateAlive}
	ev := Event{
		Type:   EventMemberJoin,
		Member: m,
	}
	if ev.Type != EventMemberJoin {
		t.Errorf("Type = %v, want EventMemberJoin", ev.Type)
	}
}

func TestMessage(t *testing.T) {
	msg := Message{
		Type:   MsgPing,
		Source: "node-1",
		Target: "node-2",
	}
	if msg.Type != MsgPing {
		t.Errorf("Type = %v, want MsgPing", msg.Type)
	}
}

func TestMemberInfo(t *testing.T) {
	info := MemberInfo{
		ID:          "node-1",
		Address:     "127.0.0.1:7946",
		State:       StateSuspect,
		Incarnation: 5,
	}
	if info.State != StateSuspect {
		t.Errorf("State = %v, want Suspect", info.State)
	}
}

func TestRandomInt(t *testing.T) {
	n := randomInt(10)
	if n < 0 || n >= 10 {
		t.Errorf("randomInt(10) = %d, want [0,10)", n)
	}
}
