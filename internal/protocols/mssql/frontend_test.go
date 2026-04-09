package mssql

import (
	"testing"
)

func TestPacketTypeConstants(t *testing.T) {
	if PackSQLBatch != 1 {
		t.Errorf("PackSQLBatch = %d, want 1", PackSQLBatch)
	}
	if PackRPCRequest != 3 {
		t.Errorf("PackRPCRequest = %d, want 3", PackRPCRequest)
	}
	if PackReply != 4 {
		t.Errorf("PackReply = %d, want 4", PackReply)
	}
	if PackPreLogin != 18 {
		t.Errorf("PackPreLogin = %d, want 18", PackPreLogin)
	}
	if PackTDS7Login != 16 {
		t.Errorf("PackTDS7Login = %d, want 16", PackTDS7Login)
	}
}

func TestStatusConstants(t *testing.T) {
	if StatusNormal != 0x00 {
		t.Errorf("StatusNormal = 0x%02x, want 0x00", StatusNormal)
	}
	if StatusEOM != 0x01 {
		t.Errorf("StatusEOM = 0x%02x, want 0x01", StatusEOM)
	}
	if StatusIgnore != 0x02 {
		t.Errorf("StatusIgnore = 0x%02x, want 0x02", StatusIgnore)
	}
	if StatusResetConn != 0x08 {
		t.Errorf("StatusResetConn = 0x%02x, want 0x08", StatusResetConn)
	}
}

func TestVersionConstants(t *testing.T) {
	if VerSQL2000 != 0x07000000 {
		t.Errorf("VerSQL2000 = 0x%08x, want 0x07000000", VerSQL2000)
	}
	if VerSQL2019 != 0x74000004 {
		t.Errorf("VerSQL2019 = 0x%08x, want 0x74000004", VerSQL2019)
	}
}

func TestSizeConstants(t *testing.T) {
	if MinLoginPacketSize != 512 {
		t.Errorf("MinLoginPacketSize = %d, want 512", MinLoginPacketSize)
	}
	if MaxLoginPacketSize != 32767 {
		t.Errorf("MaxLoginPacketSize = %d, want 32767", MaxLoginPacketSize)
	}
}

func TestClientProgVer(t *testing.T) {
	if ClientProgVer != 0x07000000 {
		t.Errorf("ClientProgVer = 0x%08x, want 0x07000000", ClientProgVer)
	}
}

func TestStateConstants(t *testing.T) {
	if StatePreLogin != 0 {
		t.Errorf("StatePreLogin = %d, want 0", StatePreLogin)
	}
	if StateLogin != 1 {
		t.Errorf("StateLogin = %d, want 1", StateLogin)
	}
	if StateReady != 2 {
		t.Errorf("StateReady = %d, want 2", StateReady)
	}
	if StateActive != 3 {
		t.Errorf("StateActive = %d, want 3", StateActive)
	}
	if StateClosed != 4 {
		t.Errorf("StateClosed = %d, want 4", StateClosed)
	}
}

func TestTDSPacket(t *testing.T) {
	pkt := &TDSPacket{
		Type:   PackSQLBatch,
		Status: StatusEOM,
		Length: 100,
		Data:   []byte("test"),
	}
	if pkt.Type != PackSQLBatch {
		t.Errorf("Type = %d, want %d", pkt.Type, PackSQLBatch)
	}
	if pkt.Status != StatusEOM {
		t.Errorf("Status = %d, want %d", pkt.Status, StatusEOM)
	}
}

func TestLogin(t *testing.T) {
	login := &Login{
		Username: "sa",
		Password: "secret",
		Database: "master",
		HostName: "localhost",
	}
	if login.Username != "sa" {
		t.Errorf("Username = %q, want sa", login.Username)
	}
	if login.Database != "master" {
		t.Errorf("Database = %q, want master", login.Database)
	}
}

func TestColumnInfo(t *testing.T) {
	col := &ColumnInfo{
		Name: "id",
		Type: 56, // INT
		Size: 4,
	}
	if col.Name != "id" {
		t.Errorf("Name = %q, want id", col.Name)
	}
	if col.Type != 56 {
		t.Errorf("Type = %d, want 56", col.Type)
	}
}
