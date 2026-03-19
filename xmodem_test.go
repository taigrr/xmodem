package xmodem

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

// mockPort simulates a serial port for testing.
type mockPort struct {
	readBuf  *bytes.Buffer
	writeBuf *bytes.Buffer
	readErr  error
	writeErr error
	flushed  bool
}

func newMockPort() *mockPort {
	return &mockPort{
		readBuf:  new(bytes.Buffer),
		writeBuf: new(bytes.Buffer),
	}
}

func (mp *mockPort) Read(p []byte) (int, error) {
	if mp.readErr != nil {
		return 0, mp.readErr
	}
	return mp.readBuf.Read(p)
}

func (mp *mockPort) Write(p []byte) (int, error) {
	if mp.writeErr != nil {
		return 0, mp.writeErr
	}
	return mp.writeBuf.Write(p)
}

func (mp *mockPort) Flush() error {
	mp.flushed = true
	return nil
}

func TestConstants(t *testing.T) {
	tests := []struct {
		name string
		got  byte
		want byte
	}{
		{"SOH", SOH, 0x01},
		{"STX", STX, 0x02},
		{"EOT", EOT, 0x04},
		{"ACK", ACK, 0x06},
		{"DLE", DLE, 0x10},
		{"NAK", NAK, 0x15},
		{"CAN", CAN, 0x18},
		{"SUB", SUB, 0x1A},
		{"CRC", CRC, 'C'},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("%s = 0x%02x, want 0x%02x", tt.name, tt.got, tt.want)
			}
		})
	}
}

func TestModes(t *testing.T) {
	if XMode128 != 0 {
		t.Errorf("XMode128 = %d, want 0", XMode128)
	}
	if XModeCRC != 1 {
		t.Errorf("XModeCRC = %d, want 1", XModeCRC)
	}
	if XMode1K != 2 {
		t.Errorf("XMode1K = %d, want 2", XMode1K)
	}
}

func TestChecksum8(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want byte
	}{
		{"empty", []byte{}, 0},
		{"single byte", []byte{0x42}, 0x42},
		{"multiple bytes", []byte{0x01, 0x02, 0x03}, 0x06},
		{"overflow wraps", []byte{0xFF, 0x01}, 0x00},
		{"all zeros", make([]byte, 128), 0x00},
		{"sequential", []byte{1, 2, 3, 4, 5, 6, 7, 8, 9}, 45},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := checksum8(tt.data)
			if got != tt.want {
				t.Errorf("checksum8(%v) = 0x%02x, want 0x%02x", tt.data, got, tt.want)
			}
		})
	}
}

func TestNewWithReadWriter(t *testing.T) {
	mock := newMockPort()
	xm := NewWithReadWriter(mock)
	if xm == nil {
		t.Fatal("NewWithReadWriter returned nil")
	}
	if xm.Padding != SUB {
		t.Errorf("Padding = 0x%02x, want 0x%02x (SUB)", xm.Padding, SUB)
	}
	if xm.Mode != XModeCRC {
		t.Errorf("Mode = %d, want %d (XModeCRC)", xm.Mode, XModeCRC)
	}
	if xm.retries != 10 {
		t.Errorf("retries = %d, want 10", xm.retries)
	}
}

func TestAbort(t *testing.T) {
	mock := newMockPort()
	xm := NewWithReadWriter(mock)
	xm.Abort()
	written := mock.writeBuf.Bytes()
	if len(written) != 2 || written[0] != CAN || written[1] != CAN {
		t.Errorf("Abort wrote %v, want [CAN CAN]", written)
	}
}

func TestSendCRCMode(t *testing.T) {
	mock := newMockPort()

	// Receiver sends CRC to request CRC mode, then ACK for each block, then ACK for EOT
	mock.readBuf.WriteByte(CRC)
	mock.readBuf.WriteByte(ACK)
	mock.readBuf.WriteByte(ACK) // EOT ACK

	xm := NewWithReadWriter(mock)
	xm.Mode = XModeCRC

	payload := bytes.NewBuffer(bytes.Repeat([]byte{0xAA}, 128))
	err := xm.Send(*payload)
	if err != nil {
		t.Fatalf("Send returned error: %v", err)
	}

	if !mock.flushed {
		t.Error("expected Flush to be called")
	}

	// Verify written data: header(3) + data(128) + crc(2) + EOT(1)
	written := mock.writeBuf.Bytes()
	if len(written) < 134 {
		t.Fatalf("wrote %d bytes, expected at least 134", len(written))
	}
	// First byte should be SOH
	if written[0] != SOH {
		t.Errorf("first byte = 0x%02x, want SOH (0x01)", written[0])
	}
	// Sequence number should be 1
	if written[1] != 1 {
		t.Errorf("sequence = %d, want 1", written[1])
	}
	// Complement should be 254
	if written[2] != 254 {
		t.Errorf("complement = %d, want 254", written[2])
	}
	// Last byte before EOT should be part of CRC
	// EOT should be the last byte
	if written[len(written)-1] != EOT {
		t.Errorf("last byte = 0x%02x, want EOT (0x04)", written[len(written)-1])
	}
}

func TestSend1KMode(t *testing.T) {
	mock := newMockPort()

	// CRC mode start, ACK per block, ACK for EOT
	mock.readBuf.WriteByte(CRC)
	mock.readBuf.WriteByte(ACK)
	mock.readBuf.WriteByte(ACK) // EOT ACK

	xm := NewWithReadWriter(mock)
	xm.Mode = XMode1K

	// Send exactly 1024 bytes
	payload := bytes.NewBuffer(bytes.Repeat([]byte{0xBB}, 1024))
	err := xm.Send(*payload)
	if err != nil {
		t.Fatalf("Send returned error: %v", err)
	}

	written := mock.writeBuf.Bytes()
	// header(3) + data(1024) + crc(2) + EOT(1) = 1030
	if len(written) < 1030 {
		t.Fatalf("wrote %d bytes, expected at least 1030", len(written))
	}
	// First byte should be STX for 1K mode
	if written[0] != STX {
		t.Errorf("first byte = 0x%02x, want STX (0x02)", written[0])
	}
}

func TestSendChecksumMode(t *testing.T) {
	mock := newMockPort()

	// NAK requests checksum mode
	mock.readBuf.WriteByte(NAK)
	mock.readBuf.WriteByte(ACK)
	mock.readBuf.WriteByte(ACK) // EOT ACK

	xm := NewWithReadWriter(mock)

	payload := bytes.NewBuffer(bytes.Repeat([]byte{0x01}, 128))
	err := xm.Send(*payload)
	if err != nil {
		t.Fatalf("Send returned error: %v", err)
	}

	written := mock.writeBuf.Bytes()
	// header(3) + data(128) + checksum(1) + EOT(1) = 133
	if len(written) < 133 {
		t.Fatalf("wrote %d bytes, expected at least 133", len(written))
	}
	// Checksum byte: 128 * 0x01 = 128 = 0x80
	checksumByte := written[131] // header(3) + data(128) = position 131
	if checksumByte != 0x80 {
		t.Errorf("checksum = 0x%02x, want 0x80", checksumByte)
	}
}

func TestSendMultipleBlocks(t *testing.T) {
	mock := newMockPort()

	// CRC start, ACK for each of 3 blocks, ACK for EOT
	mock.readBuf.WriteByte(CRC)
	mock.readBuf.WriteByte(ACK)
	mock.readBuf.WriteByte(ACK)
	mock.readBuf.WriteByte(ACK)
	mock.readBuf.WriteByte(ACK) // EOT ACK

	xm := NewWithReadWriter(mock)
	xm.Mode = XModeCRC

	// 300 bytes = 3 blocks of 128 (last block padded)
	payload := bytes.NewBuffer(bytes.Repeat([]byte{0xCC}, 300))
	err := xm.Send(*payload)
	if err != nil {
		t.Fatalf("Send returned error: %v", err)
	}
}

func TestSendPadding(t *testing.T) {
	mock := newMockPort()

	mock.readBuf.WriteByte(CRC)
	mock.readBuf.WriteByte(ACK)
	mock.readBuf.WriteByte(ACK) // EOT ACK

	xm := NewWithReadWriter(mock)
	xm.Mode = XModeCRC
	xm.Padding = 0xFF

	// 50 bytes should be padded to 128 with 0xFF
	payload := bytes.NewBuffer(bytes.Repeat([]byte{0x01}, 50))
	err := xm.Send(*payload)
	if err != nil {
		t.Fatalf("Send returned error: %v", err)
	}

	written := mock.writeBuf.Bytes()
	// Check padding area (header=3, data starts at 3, padding at 3+50=53)
	for i := 53; i < 131; i++ {
		if written[i] != 0xFF {
			t.Errorf("padding byte at %d = 0x%02x, want 0xFF", i, written[i])
			break
		}
	}
}

func TestSendCancelOnDoubleCANDuringSniff(t *testing.T) {
	mock := newMockPort()

	mock.readBuf.WriteByte(CAN)
	mock.readBuf.WriteByte(CAN)

	xm := NewWithReadWriter(mock)
	err := xm.Send(*bytes.NewBuffer([]byte{0x01}))
	if !errors.Is(err, ErrTransferCanceled) {
		t.Errorf("expected ErrTransferCanceled, got %v", err)
	}
}

func TestSendCancelOnEOTDuringSniff(t *testing.T) {
	mock := newMockPort()

	mock.readBuf.WriteByte(EOT)

	xm := NewWithReadWriter(mock)
	err := xm.Send(*bytes.NewBuffer([]byte{0x01}))
	if !errors.Is(err, ErrTransferCanceled) {
		t.Errorf("expected ErrTransferCanceled, got %v", err)
	}
}

func TestSendReadErrorDuringSniff(t *testing.T) {
	mock := newMockPort()
	mock.readErr = io.ErrUnexpectedEOF

	xm := NewWithReadWriter(mock)
	xm.retries = 1
	err := xm.Send(*bytes.NewBuffer([]byte{0x01}))
	if err == nil {
		t.Error("expected error, got nil")
	}
}

func TestSendNAKRetryThenACK(t *testing.T) {
	mock := newMockPort()

	// CRC start, NAK first attempt, ACK second, ACK for EOT
	mock.readBuf.WriteByte(CRC)
	mock.readBuf.WriteByte(NAK)
	mock.readBuf.WriteByte(ACK)
	mock.readBuf.WriteByte(ACK) // EOT ACK

	xm := NewWithReadWriter(mock)
	xm.Mode = XModeCRC

	payload := bytes.NewBuffer(bytes.Repeat([]byte{0xDD}, 128))
	err := xm.Send(*payload)
	if err != nil {
		t.Fatalf("Send returned error: %v", err)
	}
}

func TestSendEmptyPayload(t *testing.T) {
	mock := newMockPort()

	mock.readBuf.WriteByte(CRC)
	mock.readBuf.WriteByte(ACK) // EOT ACK

	xm := NewWithReadWriter(mock)

	payload := bytes.NewBuffer([]byte{})
	err := xm.Send(*payload)
	if err != nil {
		t.Fatalf("Send with empty payload returned error: %v", err)
	}
}

func TestSendSequenceWraps(t *testing.T) {
	mock := newMockPort()

	// 256 blocks to test sequence wrap-around
	mock.readBuf.WriteByte(CRC)
	for i := 0; i < 256; i++ {
		mock.readBuf.WriteByte(ACK)
	}
	mock.readBuf.WriteByte(ACK) // EOT ACK

	xm := NewWithReadWriter(mock)
	xm.Mode = XModeCRC

	// 256 * 128 = 32768 bytes
	payload := bytes.NewBuffer(bytes.Repeat([]byte{0xEE}, 256*128))
	err := xm.Send(*payload)
	if err != nil {
		t.Fatalf("Send returned error: %v", err)
	}
}

func TestErrTransferCanceled(t *testing.T) {
	if ErrTransferCanceled.Error() != "transfer canceled" {
		t.Errorf("ErrTransferCanceled = %q, want %q", ErrTransferCanceled.Error(), "transfer canceled")
	}
}
