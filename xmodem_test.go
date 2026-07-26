package xmodem

import (
	"bytes"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/taigrr/xmodem/crc16"
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

type zeroProgressPort struct{}

func (zeroProgressPort) Read(_ []byte) (int, error) {
	return 0, nil
}

func (zeroProgressPort) Write(p []byte) (int, error) {
	return len(p), nil
}

func (zeroProgressPort) Flush() error {
	return nil
}

type firstByteThenNoProgressPort struct {
	first byte
	read  bool
}

func (p *firstByteThenNoProgressPort) Read(buf []byte) (int, error) {
	if !p.read {
		p.read = true
		buf[0] = p.first
		return 1, nil
	}
	return 0, nil
}

func (p *firstByteThenNoProgressPort) Write(buf []byte) (int, error) {
	return len(buf), nil
}

func (p *firstByteThenNoProgressPort) Flush() error {
	return nil
}

// zeroThenBufPort serves bytes from pre, then returns a single zero-progress
// read (0, nil), then serves bytes from post. It counts NAK bytes written.
type zeroThenBufPort struct {
	pre      *bytes.Buffer
	post     *bytes.Buffer
	zeroDone bool
	naks     int
}

func (p *zeroThenBufPort) Read(buf []byte) (int, error) {
	if p.pre.Len() > 0 {
		return p.pre.Read(buf)
	}
	if !p.zeroDone {
		p.zeroDone = true
		return 0, nil
	}
	return p.post.Read(buf)
}

func (p *zeroThenBufPort) Write(buf []byte) (int, error) {
	for _, b := range buf {
		if b == NAK {
			p.naks++
		}
	}
	return len(buf), nil
}

func (p *zeroThenBufPort) Flush() error {
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

func TestReadFullReturnsNoProgress(t *testing.T) {
	xm := NewWithReadWriter(zeroProgressPort{})

	err := xm.readFull(make([]byte, 1))
	if !errors.Is(err, io.ErrNoProgress) {
		t.Fatalf("expected io.ErrNoProgress, got %v", err)
	}
}

func TestReadByteReturnsNoProgress(t *testing.T) {
	xm := NewWithReadWriter(zeroProgressPort{})

	_, err := xm.readByte()
	if !errors.Is(err, io.ErrNoProgress) {
		t.Fatalf("expected io.ErrNoProgress, got %v", err)
	}
}

func TestSendReturnsNoProgressAwaitingEOTACK(t *testing.T) {
	xm := NewWithReadWriter(&firstByteThenNoProgressPort{first: CRC})
	xm.retries = 1

	err := xm.Send(*bytes.NewBuffer(nil))
	if !errors.Is(err, io.ErrNoProgress) {
		t.Fatalf("expected io.ErrNoProgress, got %v", err)
	}
}

type countingZeroProgressPort struct {
	reads int
}

func (p *countingZeroProgressPort) Read(_ []byte) (int, error) {
	p.reads++
	return 0, nil
}

func (p *countingZeroProgressPort) Write(buf []byte) (int, error) {
	return len(buf), nil
}

func (p *countingZeroProgressPort) Flush() error {
	return nil
}

func TestSendProtocolSniffRespectsRetries(t *testing.T) {
	port := &countingZeroProgressPort{}
	xm := NewWithReadWriter(port)
	xm.retries = 3

	err := xm.Send(*bytes.NewBuffer(nil))
	if !errors.Is(err, io.ErrNoProgress) {
		t.Fatalf("expected io.ErrNoProgress, got %v", err)
	}
	// protocolSniff aborts once errorCount exceeds x.retries, i.e. after
	// retries+1 zero-progress reads. A hard-coded bound would over-read.
	if port.reads != xm.retries+1 {
		t.Fatalf("expected %d reads bounded by retries, got %d", xm.retries+1, port.reads)
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

// buildBlock constructs a valid XMODEM block with header, sequence, data, and CRC/checksum.
func buildBlock(header byte, seq byte, data []byte, useCRC bool) []byte {
	var block []byte
	block = append(block, header)
	block = append(block, seq, 255-seq)
	block = append(block, data...)
	if useCRC {
		crc := crc16.CRC(data, 0)
		block = append(block, byte(crc>>8), byte(crc&0xff))
	} else {
		var sum byte
		for _, b := range data {
			sum += b
		}
		block = append(block, sum)
	}
	return block
}

func TestReceiveRetriesTransientHeaderRead(t *testing.T) {
	data := bytes.Repeat([]byte{0xAA}, 128)
	block := buildBlock(SOH, 1, data, true)

	// pre serves the first block; then a one-shot zero-progress read simulates
	// a transient timeout while awaiting the next header; post then delivers the
	// two EOTs required by the double-EOT handshake.
	port := &zeroThenBufPort{
		pre:  bytes.NewBuffer(block),
		post: bytes.NewBuffer([]byte{EOT, EOT}),
	}
	xm := NewWithReadWriter(port)
	xm.Mode = XModeCRC

	var out bytes.Buffer
	if err := xm.Receive(&out); err != nil {
		t.Fatalf("Receive returned error: %v", err)
	}
	if !bytes.Equal(out.Bytes(), data) {
		t.Fatalf("received %d bytes, want %d", out.Len(), len(data))
	}
	// Two NAKs are expected: one from the transient read retry, one from the
	// first EOT of the double-EOT handshake. Crucially the transient read must
	// NOT have reprocessed a stale header.
	if port.naks != 2 {
		t.Fatalf("expected 2 NAKs (transient retry + first EOT), got %d", port.naks)
	}
}

func TestReceiveCRCMode(t *testing.T) {
	mock := newMockPort()
	xm := NewWithReadWriter(mock)
	xm.Mode = XModeCRC

	data := bytes.Repeat([]byte{0xAA}, 128)
	// Sender responds to 'C' with SOH block, then EOT
	block := buildBlock(SOH, 1, data, true)
	mock.readBuf.Write(block)
	mock.readBuf.WriteByte(EOT)
	mock.readBuf.WriteByte(EOT)

	var out bytes.Buffer
	err := xm.Receive(&out)
	if err != nil {
		t.Fatalf("Receive returned error: %v", err)
	}

	if !mock.flushed {
		t.Error("expected Flush to be called")
	}

	if !bytes.Equal(out.Bytes(), data) {
		t.Errorf("received %d bytes, want %d", out.Len(), len(data))
	}

	// Verify handshake: first byte written should be 'C'
	written := mock.writeBuf.Bytes()
	if len(written) == 0 || written[0] != CRC {
		t.Errorf("first written byte = 0x%02x, want 'C' (0x43)", written[0])
	}
}

func TestReceiveChecksumMode(t *testing.T) {
	mock := newMockPort()
	xm := NewWithReadWriter(mock)
	xm.Mode = XMode128

	data := bytes.Repeat([]byte{0x01}, 128)
	block := buildBlock(SOH, 1, data, false)
	mock.readBuf.Write(block)
	mock.readBuf.WriteByte(EOT)
	mock.readBuf.WriteByte(EOT)

	var out bytes.Buffer
	err := xm.Receive(&out)
	if err != nil {
		t.Fatalf("Receive returned error: %v", err)
	}

	if !bytes.Equal(out.Bytes(), data) {
		t.Errorf("received data mismatch")
	}

	// First written byte should be NAK for checksum mode
	written := mock.writeBuf.Bytes()
	if len(written) == 0 || written[0] != NAK {
		t.Errorf("first written byte = 0x%02x, want NAK (0x15)", written[0])
	}
}

func TestReceive1KMode(t *testing.T) {
	mock := newMockPort()
	xm := NewWithReadWriter(mock)
	xm.Mode = XMode1K

	data := bytes.Repeat([]byte{0xBB}, 1024)
	block := buildBlock(STX, 1, data, true)
	mock.readBuf.Write(block)
	mock.readBuf.WriteByte(EOT)
	mock.readBuf.WriteByte(EOT)

	var out bytes.Buffer
	err := xm.Receive(&out)
	if err != nil {
		t.Fatalf("Receive returned error: %v", err)
	}

	if out.Len() != 1024 {
		t.Errorf("received %d bytes, want 1024", out.Len())
	}
}

func TestReceiveMultipleBlocks(t *testing.T) {
	mock := newMockPort()
	xm := NewWithReadWriter(mock)
	xm.Mode = XModeCRC

	for seq := byte(1); seq <= 3; seq++ {
		data := bytes.Repeat([]byte{seq}, 128)
		block := buildBlock(SOH, seq, data, true)
		mock.readBuf.Write(block)
	}
	mock.readBuf.WriteByte(EOT)
	mock.readBuf.WriteByte(EOT)

	var out bytes.Buffer
	err := xm.Receive(&out)
	if err != nil {
		t.Fatalf("Receive returned error: %v", err)
	}

	if out.Len() != 384 {
		t.Errorf("received %d bytes, want 384", out.Len())
	}

	// Verify block contents
	for seq := byte(1); seq <= 3; seq++ {
		offset := int(seq-1) * 128
		expected := bytes.Repeat([]byte{seq}, 128)
		if !bytes.Equal(out.Bytes()[offset:offset+128], expected) {
			t.Errorf("block %d data mismatch", seq)
		}
	}
}

func TestReceiveCancelOnDoubleCANDuringHandshake(t *testing.T) {
	mock := newMockPort()
	xm := NewWithReadWriter(mock)

	mock.readBuf.WriteByte(CAN)

	var out bytes.Buffer
	err := xm.Receive(&out)
	if !errors.Is(err, ErrTransferCanceled) {
		t.Errorf("expected ErrTransferCanceled, got %v", err)
	}
}

func TestReceiveEOTImmediately(t *testing.T) {
	mock := newMockPort()
	xm := NewWithReadWriter(mock)

	mock.readBuf.WriteByte(EOT)
	mock.readBuf.WriteByte(EOT)

	var out bytes.Buffer
	err := xm.Receive(&out)
	if err != nil {
		t.Fatalf("Receive returned error: %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("received %d bytes, want 0", out.Len())
	}
	// The first EOT must be answered with a NAK before the second is ACK'd.
	written := mock.writeBuf.Bytes()
	if written[len(written)-1] != ACK {
		t.Errorf("last written byte = 0x%02x, want ACK", written[len(written)-1])
	}
}

func TestReceiveHandshakeTimeout(t *testing.T) {
	mock := newMockPort()
	mock.readErr = io.ErrUnexpectedEOF

	xm := NewWithReadWriter(mock)
	xm.retries = 2

	var out bytes.Buffer
	err := xm.Receive(&out)
	if err == nil {
		t.Error("expected error, got nil")
	}
}

func TestReceiveBadCRC(t *testing.T) {
	mock := newMockPort()
	xm := NewWithReadWriter(mock)
	xm.Mode = XModeCRC
	xm.retries = 1

	data := bytes.Repeat([]byte{0xAA}, 128)
	// Build a block with corrupted CRC
	var block []byte
	block = append(block, SOH, 1, 254)
	block = append(block, data...)
	block = append(block, 0xFF, 0xFF) // bad CRC
	mock.readBuf.Write(block)

	// After NAK, sender gives up (read error)
	// Second attempt also bad
	mock.readBuf.Write(block)

	var out bytes.Buffer
	err := xm.Receive(&out)
	if err == nil {
		t.Error("expected error from bad CRC, got nil")
	}
	if !errors.Is(err, ErrChecksumMismatch) {
		t.Errorf("expected ErrChecksumMismatch, got %v", err)
	}
}

func TestReceiveDoubleCANDuringTransfer(t *testing.T) {
	mock := newMockPort()
	xm := NewWithReadWriter(mock)
	xm.Mode = XModeCRC

	// One good block, then double CAN
	data := bytes.Repeat([]byte{0xAA}, 128)
	block := buildBlock(SOH, 1, data, true)
	mock.readBuf.Write(block)
	mock.readBuf.WriteByte(CAN)
	mock.readBuf.WriteByte(CAN)

	var out bytes.Buffer
	err := xm.Receive(&out)
	if !errors.Is(err, ErrTransferCanceled) {
		t.Errorf("expected ErrTransferCanceled, got %v", err)
	}
	// First block should still have been received
	if out.Len() != 128 {
		t.Errorf("received %d bytes before cancel, want 128", out.Len())
	}
}

// buildBlockRaw constructs a block with an explicitly supplied sequence and
// complement byte, allowing malformed blocks to be built for testing.
func buildBlockRaw(header, seq, comp byte, data []byte, useCRC bool) []byte {
	block := []byte{header, seq, comp}
	block = append(block, data...)
	if useCRC {
		crc := crc16.CRC(data, 0)
		block = append(block, byte(crc>>8), byte(crc&0xff))
	} else {
		var sum byte
		for _, b := range data {
			sum += b
		}
		block = append(block, sum)
	}
	return block
}

// oneBytePort serves its read buffer exactly one byte per Read call, forcing
// readFull to reassemble partial reads.
type oneBytePort struct {
	readBuf  *bytes.Buffer
	writeBuf *bytes.Buffer
}

func (p *oneBytePort) Read(buf []byte) (int, error) {
	if p.readBuf.Len() == 0 {
		return 0, io.EOF
	}
	if len(buf) == 0 {
		return 0, nil
	}
	b, _ := p.readBuf.ReadByte()
	buf[0] = b
	return 1, nil
}

func (p *oneBytePort) Write(buf []byte) (int, error) {
	return p.writeBuf.Write(buf)
}

func (p *oneBytePort) Flush() error { return nil }

// crcTimeoutPort returns a fixed number of zero-progress reads (simulating an
// unanswered 'C' handshake) before serving its read buffer, and records writes.
type crcTimeoutPort struct {
	timeouts int
	readBuf  *bytes.Buffer
	writeBuf *bytes.Buffer
}

func (p *crcTimeoutPort) Read(buf []byte) (int, error) {
	if p.timeouts > 0 {
		p.timeouts--
		return 0, nil
	}
	return p.readBuf.Read(buf)
}

func (p *crcTimeoutPort) Write(buf []byte) (int, error) {
	return p.writeBuf.Write(buf)
}

func (p *crcTimeoutPort) Flush() error { return nil }

// flakyWritePort fails the first failWrites Write calls, then succeeds.
type flakyWritePort struct {
	failWrites int
	readBuf    *bytes.Buffer
	writeBuf   *bytes.Buffer
}

func (p *flakyWritePort) Read(buf []byte) (int, error) {
	return p.readBuf.Read(buf)
}

func (p *flakyWritePort) Write(buf []byte) (int, error) {
	if p.failWrites > 0 {
		p.failWrites--
		return 0, io.ErrShortWrite
	}
	return p.writeBuf.Write(buf)
}

func (p *flakyWritePort) Flush() error { return nil }

func TestReceiveDuplicateBlock(t *testing.T) {
	mock := newMockPort()
	xm := NewWithReadWriter(mock)
	xm.Mode = XModeCRC

	data := bytes.Repeat([]byte{0xAA}, 128)
	block := buildBlock(SOH, 1, data, true)
	// Same block twice: the second is a duplicate (sender missed our ACK).
	mock.readBuf.Write(block)
	mock.readBuf.Write(block)
	mock.readBuf.WriteByte(EOT)
	mock.readBuf.WriteByte(EOT)

	var out bytes.Buffer
	if err := xm.Receive(&out); err != nil {
		t.Fatalf("Receive returned error: %v", err)
	}
	// Duplicate must be ACK'd but not written.
	if out.Len() != 128 {
		t.Errorf("received %d bytes, want 128 (duplicate discarded)", out.Len())
	}
	if !bytes.Equal(out.Bytes(), data) {
		t.Errorf("received data mismatch")
	}
}

func TestReceiveComplementMismatchThenRecovery(t *testing.T) {
	mock := newMockPort()
	xm := NewWithReadWriter(mock)
	xm.Mode = XModeCRC

	data := bytes.Repeat([]byte{0x5A}, 128)
	// Block with a corrupted complement byte, then a valid retransmit.
	bad := buildBlockRaw(SOH, 1, 200, data, true)
	good := buildBlock(SOH, 1, data, true)
	mock.readBuf.Write(bad)
	mock.readBuf.Write(good)
	mock.readBuf.WriteByte(EOT)
	mock.readBuf.WriteByte(EOT)

	var out bytes.Buffer
	if err := xm.Receive(&out); err != nil {
		t.Fatalf("Receive returned error: %v", err)
	}
	if !bytes.Equal(out.Bytes(), data) {
		t.Errorf("received data mismatch after recovery")
	}
	// A NAK must have been sent for the malformed block.
	if !bytes.Contains(mock.writeBuf.Bytes(), []byte{NAK}) {
		t.Error("expected a NAK to be sent for the malformed block")
	}
}

func TestReceiveBadCRCThenRecovery(t *testing.T) {
	mock := newMockPort()
	xm := NewWithReadWriter(mock)
	xm.Mode = XModeCRC

	data := bytes.Repeat([]byte{0x11}, 128)
	bad := buildBlockRaw(SOH, 1, 254, data, true)
	bad[len(bad)-1] ^= 0xFF // corrupt CRC low byte
	good := buildBlock(SOH, 1, data, true)
	mock.readBuf.Write(bad)
	mock.readBuf.Write(good)
	mock.readBuf.WriteByte(EOT)
	mock.readBuf.WriteByte(EOT)

	var out bytes.Buffer
	if err := xm.Receive(&out); err != nil {
		t.Fatalf("Receive returned error: %v", err)
	}
	if !bytes.Equal(out.Bytes(), data) {
		t.Errorf("received data mismatch after CRC recovery")
	}
}

func TestReceiveChecksumModeBadChecksum(t *testing.T) {
	mock := newMockPort()
	xm := NewWithReadWriter(mock)
	xm.Mode = XMode128
	xm.retries = 0

	data := bytes.Repeat([]byte{0x22}, 128)
	block := buildBlock(SOH, 1, data, false)
	block[len(block)-1] ^= 0xFF // corrupt checksum
	mock.readBuf.Write(block)

	var out bytes.Buffer
	err := xm.Receive(&out)
	if !errors.Is(err, ErrChecksumMismatch) {
		t.Errorf("expected ErrChecksumMismatch, got %v", err)
	}
}

func TestReceiveSequenceMismatchFatal(t *testing.T) {
	mock := newMockPort()
	xm := NewWithReadWriter(mock)
	xm.Mode = XModeCRC
	xm.retries = 0

	data := bytes.Repeat([]byte{0x33}, 128)
	// Sequence 5 when expecting 1 (not a duplicate) is a fatal desync.
	block := buildBlock(SOH, 5, data, true)
	mock.readBuf.Write(block)

	var out bytes.Buffer
	err := xm.Receive(&out)
	if !errors.Is(err, ErrSequenceMismatch) {
		t.Errorf("expected ErrSequenceMismatch, got %v", err)
	}
}

func TestReceiveFirstEOTThenResume(t *testing.T) {
	mock := newMockPort()
	xm := NewWithReadWriter(mock)
	xm.Mode = XModeCRC

	data := bytes.Repeat([]byte{0x44}, 128)
	// A spurious EOT arrives first; the receiver NAKs, then real data follows.
	mock.readBuf.WriteByte(EOT)
	mock.readBuf.Write(buildBlock(SOH, 1, data, true))
	mock.readBuf.WriteByte(EOT)
	mock.readBuf.WriteByte(EOT)

	var out bytes.Buffer
	if err := xm.Receive(&out); err != nil {
		t.Fatalf("Receive returned error: %v", err)
	}
	if !bytes.Equal(out.Bytes(), data) {
		t.Errorf("expected to resume and receive data after spurious EOT")
	}
}

func TestReceiveCRCFallbackToChecksum(t *testing.T) {
	port := &crcTimeoutPort{
		timeouts: crcHandshakeAttempts,
		readBuf:  new(bytes.Buffer),
		writeBuf: new(bytes.Buffer),
	}
	xm := NewWithReadWriter(port)
	xm.Mode = XModeCRC

	data := bytes.Repeat([]byte{0x66}, 128)
	// After falling back, the sender uses checksum-mode blocks.
	port.readBuf.Write(buildBlock(SOH, 1, data, false))
	port.readBuf.WriteByte(EOT)
	port.readBuf.WriteByte(EOT)

	var out bytes.Buffer
	if err := xm.Receive(&out); err != nil {
		t.Fatalf("Receive returned error: %v", err)
	}
	if !bytes.Equal(out.Bytes(), data) {
		t.Errorf("received data mismatch after CRC->checksum fallback")
	}
	// Handshake should have sent crcHandshakeAttempts 'C' bytes, then a NAK.
	written := port.writeBuf.Bytes()
	cCount := bytes.Count(written, []byte{CRC})
	if cCount != crcHandshakeAttempts {
		t.Errorf("sent %d 'C' bytes, want %d", cCount, crcHandshakeAttempts)
	}
	if !bytes.Contains(written, []byte{NAK}) {
		t.Error("expected a NAK after CRC fallback")
	}
}

func TestReceivePartialReadsReassembled(t *testing.T) {
	port := &oneBytePort{
		readBuf:  new(bytes.Buffer),
		writeBuf: new(bytes.Buffer),
	}
	xm := NewWithReadWriter(port)
	xm.Mode = XModeCRC

	data := bytes.Repeat([]byte{0x77}, 128)
	port.readBuf.Write(buildBlock(SOH, 1, data, true))
	port.readBuf.WriteByte(EOT)
	port.readBuf.WriteByte(EOT)

	var out bytes.Buffer
	if err := xm.Receive(&out); err != nil {
		t.Fatalf("Receive returned error: %v", err)
	}
	if !bytes.Equal(out.Bytes(), data) {
		t.Errorf("partial-read reassembly failed")
	}
}

func TestSendSingleCANThenContinue(t *testing.T) {
	mock := newMockPort()
	// A lone CAN during sniff must not cancel; transfer proceeds on CRC.
	mock.readBuf.WriteByte(CAN)
	mock.readBuf.WriteByte(CRC)
	mock.readBuf.WriteByte(ACK)
	mock.readBuf.WriteByte(ACK) // EOT ACK

	xm := NewWithReadWriter(mock)
	xm.Mode = XModeCRC

	if err := xm.Send(*bytes.NewBuffer(bytes.Repeat([]byte{0x88}, 128))); err != nil {
		t.Fatalf("Send returned error: %v", err)
	}
}

func TestSendEOTNAKThenACK(t *testing.T) {
	mock := newMockPort()
	// EOT is NAK'd once (sender must resend), then ACK'd.
	mock.readBuf.WriteByte(CRC)
	mock.readBuf.WriteByte(ACK)
	mock.readBuf.WriteByte(NAK) // NAK the first EOT
	mock.readBuf.WriteByte(ACK) // ACK the resent EOT

	xm := NewWithReadWriter(mock)
	xm.Mode = XModeCRC

	if err := xm.Send(*bytes.NewBuffer(bytes.Repeat([]byte{0x99}, 128))); err != nil {
		t.Fatalf("Send returned error: %v", err)
	}
	// Two EOT bytes should have been written (original + resend).
	if n := bytes.Count(mock.writeBuf.Bytes(), []byte{EOT}); n != 2 {
		t.Errorf("wrote %d EOT bytes, want 2 (resend after NAK)", n)
	}
}

func TestSendPacketWriteErrorRetry(t *testing.T) {
	port := &flakyWritePort{
		failWrites: 1,
		readBuf:    new(bytes.Buffer),
		writeBuf:   new(bytes.Buffer),
	}
	port.readBuf.WriteByte(CRC)
	port.readBuf.WriteByte(ACK)
	port.readBuf.WriteByte(ACK) // EOT ACK

	xm := NewWithReadWriter(port)
	xm.Mode = XModeCRC

	if err := xm.Send(*bytes.NewBuffer(bytes.Repeat([]byte{0xAB}, 128))); err != nil {
		t.Fatalf("Send returned error: %v", err)
	}
}

func TestSendChecksumMultipleBlocks(t *testing.T) {
	mock := newMockPort()
	mock.readBuf.WriteByte(NAK) // checksum mode
	mock.readBuf.WriteByte(ACK)
	mock.readBuf.WriteByte(ACK)
	mock.readBuf.WriteByte(ACK) // EOT ACK

	xm := NewWithReadWriter(mock)

	// 200 bytes -> 2 checksum blocks (second padded).
	if err := xm.Send(*bytes.NewBuffer(bytes.Repeat([]byte{0x0F}, 200))); err != nil {
		t.Fatalf("Send returned error: %v", err)
	}
}

func TestReceiveWireFramingAllModes(t *testing.T) {
	// Decodes valid wire framing for every mode to confirm Receive interoperates.
	for _, mode := range []Mode{XMode128, XModeCRC, XMode1K} {
		payload := bytes.Repeat([]byte{0x3C}, 700)

		sendOut := new(bytes.Buffer)
		// Capture what Send would transmit by feeding it canned ACKs; instead
		// build the wire framing directly and verify Receive decodes it.
		useCRC := mode != XMode128
		header := byte(SOH)
		blockSize := 128
		if mode == XMode1K {
			header = STX
			blockSize = 1024
		}
		seq := byte(1)
		for off := 0; off < len(payload); off += blockSize {
			end := off + blockSize
			block := make([]byte, blockSize)
			copy(block, payload[off:min(end, len(payload))])
			for i := len(payload) - off; i < blockSize; i++ {
				block[i] = SUB
			}
			sendOut.Write(buildBlock(header, seq, block, useCRC))
			seq++
		}
		sendOut.WriteByte(EOT)
		sendOut.WriteByte(EOT)

		mock := newMockPort()
		mock.readBuf = sendOut
		xm := NewWithReadWriter(mock)
		xm.Mode = mode

		var out bytes.Buffer
		if err := xm.Receive(&out); err != nil {
			t.Fatalf("mode %d: Receive error: %v", mode, err)
		}
		if !bytes.HasPrefix(out.Bytes(), payload) {
			t.Errorf("mode %d: received data does not contain payload", mode)
		}
	}
}

func TestReceiveSingleEOTCompletes(t *testing.T) {
	// A sender that transmits a single EOT and does not resend on NAK must
	// still complete successfully with all data intact.
	mock := newMockPort()
	xm := NewWithReadWriter(mock)
	xm.Mode = XModeCRC

	data := bytes.Repeat([]byte{0xC3}, 128)
	mock.readBuf.Write(buildBlock(SOH, 1, data, true))
	mock.readBuf.WriteByte(EOT) // only one EOT

	var out bytes.Buffer
	if err := xm.Receive(&out); err != nil {
		t.Fatalf("Receive returned error for single-EOT sender: %v", err)
	}
	if !bytes.Equal(out.Bytes(), data) {
		t.Errorf("received data mismatch")
	}
	// The transfer must still be ACK'd despite the single EOT.
	written := mock.writeBuf.Bytes()
	if written[len(written)-1] != ACK {
		t.Errorf("last written byte = 0x%02x, want ACK", written[len(written)-1])
	}
}

func TestReceive1KNoChecksumFallback(t *testing.T) {
	// XMODEM-1K is CRC-only: unanswered 'C' handshakes must never fall back to
	// checksum/NAK mode.
	port := &crcTimeoutPort{
		timeouts: crcHandshakeAttempts + 2,
		readBuf:  new(bytes.Buffer),
		writeBuf: new(bytes.Buffer),
	}
	xm := NewWithReadWriter(port)
	xm.Mode = XMode1K

	data := bytes.Repeat([]byte{0xD4}, 1024)
	port.readBuf.Write(buildBlock(STX, 1, data, true))
	port.readBuf.WriteByte(EOT)
	port.readBuf.WriteByte(EOT)

	var out bytes.Buffer
	if err := xm.Receive(&out); err != nil {
		t.Fatalf("Receive returned error: %v", err)
	}
	if !bytes.Equal(out.Bytes(), data) {
		t.Errorf("received data mismatch in 1K mode")
	}
	// It must keep sending 'C' past crcHandshakeAttempts rather than switching
	// to NAK/checksum handshakes (a checksum fallback would misdecode the
	// CRC-framed 1K block and fail before reaching here).
	if n := bytes.Count(port.writeBuf.Bytes(), []byte{CRC}); n <= crcHandshakeAttempts {
		t.Errorf("sent %d 'C' handshake bytes, want > %d (no fallback)", n, crcHandshakeAttempts)
	}
}

// repeatBlockPort serves the same block forever and discards writes, modeling a
// sender stuck resending an already-received block (never sees our ACKs).
type repeatBlockPort struct {
	block []byte
	pos   int
}

func (p *repeatBlockPort) Read(buf []byte) (int, error) {
	if len(buf) == 0 {
		return 0, nil
	}
	buf[0] = p.block[p.pos]
	p.pos = (p.pos + 1) % len(p.block)
	return 1, nil
}

func (p *repeatBlockPort) Write(buf []byte) (int, error) { return len(buf), nil }
func (p *repeatBlockPort) Flush() error                  { return nil }

func TestReceiveDuplicateLivelockBounded(t *testing.T) {
	// A sender that endlessly resends the same block must not loop forever;
	// the duplicate retry ceiling has to abort the transfer.
	data := bytes.Repeat([]byte{0xA5}, 128)
	port := &repeatBlockPort{block: buildBlock(SOH, 1, data, true)}
	xm := NewWithReadWriter(port)
	xm.Mode = XModeCRC
	xm.retries = 5

	done := make(chan error, 1)
	go func() {
		var out bytes.Buffer
		done <- xm.Receive(&out)
	}()

	select {
	case err := <-done:
		if !errors.Is(err, ErrTransferCanceled) {
			t.Fatalf("expected ErrTransferCanceled from bounded livelock, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Receive did not terminate: duplicate livelock is unbounded")
	}
}
