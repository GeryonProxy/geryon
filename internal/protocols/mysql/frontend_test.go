package mysql

import (
	"testing"
)

func TestCommandConstants(t *testing.T) {
	if ComSleep != 0x00 {
		t.Errorf("ComSleep = 0x%02x, want 0x00", ComSleep)
	}
	if ComQuit != 0x01 {
		t.Errorf("ComQuit = 0x%02x, want 0x01", ComQuit)
	}
	if ComInitDB != 0x02 {
		t.Errorf("ComInitDB = 0x%02x, want 0x02", ComInitDB)
	}
	if ComQuery != 0x03 {
		t.Errorf("ComQuery = 0x%02x, want 0x03", ComQuery)
	}
	if ComPing != 0x0e {
		t.Errorf("ComPing = 0x%02x, want 0x0e", ComPing)
	}
	if ComStmtPrepare != 0x16 {
		t.Errorf("ComStmtPrepare = 0x%02x, want 0x16", ComStmtPrepare)
	}
	if ComStmtExecute != 0x17 {
		t.Errorf("ComStmtExecute = 0x%02x, want 0x17", ComStmtExecute)
	}
	if ComStmtClose != 0x19 {
		t.Errorf("ComStmtClose = 0x%02x, want 0x19", ComStmtClose)
	}
}

func TestClientCapabilityConstants(t *testing.T) {
	if ClientLongPassword != 1<<0 {
		t.Error("ClientLongPassword should be 1<<0")
	}
	if ClientProtocol41 != 1<<9 {
		t.Error("ClientProtocol41 should be 1<<9")
	}
	if ClientSSL != 1<<11 {
		t.Error("ClientSSL should be 1<<11")
	}
	if ClientTransactions != 1<<13 {
		t.Error("ClientTransactions should be 1<<13")
	}
}

func TestServerStatusConstants(t *testing.T) {
	if ServerStatusInTransaction != 1<<0 {
		t.Error("ServerStatusInTransaction should be 1<<0")
	}
	if ServerStatusAutocommit != 1<<1 {
		t.Error("ServerStatusAutocommit should be 1<<1")
	}
}

func TestResponseConstants(t *testing.T) {
	if OK != 0x00 {
		t.Errorf("OK = 0x%02x, want 0x00", OK)
	}
	if EOF != 0xfe {
		t.Errorf("EOF = 0x%02x, want 0xfe", EOF)
	}
	if ERR != 0xff {
		t.Errorf("ERR = 0x%02x, want 0xff", ERR)
	}
}

func TestProtocolVersion(t *testing.T) {
	if ProtocolVersion != 10 {
		t.Errorf("ProtocolVersion = %d, want 10", ProtocolVersion)
	}
}

func TestStateConstants(t *testing.T) {
	if StateHandshake != 0 {
		t.Errorf("StateHandshake = %d, want 0", StateHandshake)
	}
	if StateAuthentication != 1 {
		t.Errorf("StateAuthentication = %d, want 1", StateAuthentication)
	}
	if StateReady != 2 {
		t.Errorf("StateReady = %d, want 2", StateReady)
	}
	if StateQuery != 3 {
		t.Errorf("StateQuery = %d, want 3", StateQuery)
	}
	if StateClosed != 4 {
		t.Errorf("StateClosed = %d, want 4", StateClosed)
	}
}

func TestPreparedStatement(t *testing.T) {
	stmt := &PreparedStatement{
		ID:         1,
		Query:      "SELECT ?",
		ParamCount: 1,
	}
	if stmt.ID != 1 {
		t.Errorf("ID = %d, want 1", stmt.ID)
	}
}

func TestColumnInfo(t *testing.T) {
	col := &ColumnInfo{
		Name:      "id",
		Type:      3, // MYSQL_TYPE_LONG
		Charset:   63,
		ColumnLen: 11,
	}
	if col.Name != "id" {
		t.Errorf("Name = %q, want id", col.Name)
	}
	if col.Type != 3 {
		t.Errorf("Type = %d, want 3", col.Type)
	}
}
