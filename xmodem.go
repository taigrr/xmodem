// Package xmodem implements the XMODEM file transfer protocol with support
// for XMODEM-128 (checksum), XMODEM-CRC, and XMODEM-1K variants.
package xmodem

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/taigrr/log-socket/log"
	"github.com/tarm/serial"

	"github.com/taigrr/xmodem/crc16"
)

// XMODEM protocol control bytes.
const (
	SOH = 0x01 // Start of Header (128-byte blocks)
	STX = 0x02 // Start of Text (1024-byte blocks)
	EOT = 0x04 // End of Transmission
	ACK = 0x06 // Acknowledge
	DLE = 0x10 // Data Link Escape
	NAK = 0x15 // Negative Acknowledge
	CAN = 0x18 // Cancel
	SUB = 0x1A // Substitute (default padding byte)
	CRC = 'C'  // CRC mode request
)

// Mode selects the XMODEM variant for a transfer.
const (
	XMode128 Mode = iota // XMODEM-128 with 8-bit checksum
	XModeCRC             // XMODEM-CRC with 16-bit CRC
	XMode1K              // XMODEM-1K with 1024-byte blocks and CRC
)

type (
	// Mode represents the XMODEM protocol variant.
	Mode int

	// Port abstracts a serial port for reading, writing, and flushing.
	// *serial.Port satisfies this interface.
	Port interface {
		io.ReadWriter
		Flush() error
	}

	// Xmodem manages an XMODEM file transfer over a Port.
	Xmodem struct {
		port    Port
		Padding byte
		Mode    Mode
		retries int
		Timeout time.Duration
	}
)

var (
	// ErrTransferCanceled indicates the transfer was canceled by the remote end
	// or aborted due to too many errors.
	ErrTransferCanceled = errors.New("transfer canceled")

	// ErrSequenceMismatch indicates a received block had an unexpected sequence number.
	ErrSequenceMismatch = errors.New("sequence number mismatch")

	// ErrChecksumMismatch indicates a received block failed checksum or CRC verification.
	ErrChecksumMismatch = errors.New("checksum mismatch")
)

func (x Xmodem) Abort() {
	x.port.Write([]byte{CAN, CAN})
}

// NewWithPort creates an Xmodem instance from an existing serial port.
func NewWithPort(port *serial.Port) *Xmodem {
	return newWithPort(port)
}

// NewWithReadWriter creates an Xmodem instance from any Port implementation.
// This is useful for testing or non-serial transports.
func NewWithReadWriter(port Port) *Xmodem {
	return newWithPort(port)
}

func newWithPort(port Port) *Xmodem {
	return &Xmodem{
		port:    port,
		Padding: SUB,
		Mode:    XModeCRC,
		retries: 10,
		Timeout: time.Second * 5,
	}
}

func New(port string, baud int) (*Xmodem, error) {
	portConfig := &serial.Config{
		Name:        port,
		Baud:        baud,
		ReadTimeout: time.Duration(5) * time.Second,
		Size:        serial.DefaultSize,
		Parity:      serial.ParityNone,
		StopBits:    serial.Stop1,
	}
	p, err := serial.OpenPort(portConfig)
	if err != nil {
		return nil, err
	}
	return &Xmodem{
		port:    p,
		Padding: SUB,
		Mode:    XModeCRC,
		retries: 10,
		Timeout: time.Second * 5,
	}, nil
}

// checksum8 computes a simple 8-bit checksum (sum of all bytes mod 256).
func checksum8(data []byte) byte {
	var sum byte
	for _, b := range data {
		sum += b
	}
	return sum
}

// Send transmits the payload using the XMODEM protocol.
// The receiver initiates the transfer by sending NAK (checksum mode) or
// 'C' (CRC mode). Blocks are retransmitted on NAK up to the configured
// retry limit.
func (x Xmodem) Send(payload bytes.Buffer) error {
	var (
		errorCount = 0
		cancel     = 0
		bytePacket = make([]byte, 1)
		totalSent  = 0
	)
	x.port.Flush()
protocolSniff:
	for {
		// Listen for first NAK or CRC
		_, err := x.port.Read(bytePacket)
		if err != nil {
			log.Errorf("Error reading from port: %v", err)
			errorCount++
			if errorCount > 10 {
				log.Errorf("Too many errors, aborting transfer")
				return err
			}
			continue
		}
		switch bytePacket[0] {
		case NAK:
			log.Tracef("standard checksum requested (NAK).\n")
			x.Mode = XMode128
			break protocolSniff
		case CRC:
			log.Tracef("16-bit CRC requested (CRC).\n")
			if x.Mode == XMode128 {
				x.Mode = XModeCRC
			}
			break protocolSniff
		case CAN:
			if cancel != 0 {
				log.Errorf("Transmission canceled: received CAN CAN at start-sequence\n")
				return ErrTransferCanceled
			}
			cancel = 1
		case EOT:
			log.Errorf("Transmission canceled: received EOT at start-sequence\n")
			return ErrTransferCanceled
		default:
			log.Debugf("Expected NAK, CRC, CAN, or EOT, got %v", bytePacket[0])
			errorCount++
			if errorCount > x.retries {
				log.Errorf("send error: error_count reached %d, aborting.\n", x.retries)
				return ErrTransferCanceled
			}
		}
	}

	sequence := 1
	packetSize := 128
	if x.Mode == XMode1K {
		packetSize = 1024
	}

	log.Tracef("Sending %d bytes in %d byte blocks", payload.Len(), packetSize)
	for {
		data := make([]byte, packetSize)
		n, err := payload.Read(data)
		if err != nil || n == 0 {
			log.Printf("send: at EOF")
			break
		} else if n < packetSize {
			log.Tracef("send: short read, padding with %d bytes\n", packetSize-n)
			for i := n; i < packetSize; i++ {
				data[i] = x.Padding
			}
		}
		header := make([]byte, 3)
		if x.Mode == XMode1K {
			header[0] = STX
		} else {
			header[0] = SOH
		}
		header[1] = byte(sequence)
		header[2] = byte(255 - sequence)
		var checkSum []byte
		if x.Mode == XMode128 {
			cs := checksum8(data)
			checkSum = []byte{cs}
		} else {
			cs := crc16.CRC(data, 0)
			checkSum = make([]byte, 2)
			checkSum[0] = byte(cs >> 8)
			checkSum[1] = byte(cs & 0xff)
		}
	sendLoop:
		for {
			if totalSent%100 == 0 {
				log.Printf("send: block %d\n", totalSent)
			}
			packet := append(header, data...)
			packet = append(packet, checkSum...)
			_, err := x.port.Write(packet)
			if err != nil {
				log.Errorf("Error writing packet: %v", err)
				errorCount++
				if errorCount > x.retries {
					log.Errorf("send error: error_count reached %d, aborting.\n", x.retries)
					return ErrTransferCanceled
				}
				continue
			}

			// Listen for ACK or NAK
			_, err = x.port.Read(bytePacket)
			if err != nil {
				log.Errorf("Error reading from port: %v", err)
				errorCount++
				if errorCount > x.retries {
					log.Errorf("Too many errors, aborting transfer")
					return err
				}
				continue
			}
			switch bytePacket[0] {
			case ACK:
				errorCount = 0
				sequence = (sequence + 1) % 256
				totalSent++
				break sendLoop
			case NAK:
				log.Errorf("send error: NAK received for block %d", totalSent)
				errorCount++
				if errorCount > x.retries {
					log.Error("Too many errors, aborting transfer", errorCount)
					return ErrTransferCanceled
				}
			default:
				log.Errorf("send error: expected ACK or NAK, got %v", bytePacket[0])
				errorCount++
				if errorCount > x.retries {
					log.Error("Too many errors, aborting transfer", errorCount)
					return ErrTransferCanceled
				}
			}
		}
	}
	log.Printf("sent final: block %d\n", totalSent)
	for {
		log.Info("sending EOT, awaiting ACK")
		// End of transmission
		_, err := x.port.Write([]byte{EOT})
		if err != nil {
			log.Errorf("Error writing EOT: %v", err)
			errorCount++
			if errorCount > x.retries {
				log.Errorf("Too many errors, aborting transfer")
				return err
			}
			continue
		}
		log.Info("EOT sent")
		// An ACK should be returned
		n, err := x.port.Read(bytePacket)
		if err != nil || n == 0 {
			log.Errorf("Error reading from port: %v", err)
			errorCount++
			if errorCount > x.retries {
				log.Errorf("Too many errors, aborting transfer")
				return err
			}
			continue
		} else {
			log.Tracef("Received `%v`", bytePacket[0])
		}
		switch bytePacket[0] {
		case ACK:
			log.Info("ACK received, transmission successful")
			return nil
		default:
			log.Errorf("send error: expected ACK; got %v\n", bytePacket[0])
			errorCount++
			if errorCount > x.retries {
				log.Errorf("EOT was not ACKd, aborting transfer")
				return ErrTransferCanceled
			}
		}
	}
}

// readFull reads exactly len(buf) bytes from the port, retrying partial reads.
func (x Xmodem) readFull(buf []byte) error {
	offset := 0
	for offset < len(buf) {
		n, err := x.port.Read(buf[offset:])
		if err != nil {
			return err
		}
		offset += n
	}
	return nil
}

// receiveBlock reads and validates a single XMODEM block after the header byte.
// Returns the validated data payload, or an error. On recoverable errors it
// returns nil data (caller should NAK and retry).
func (x Xmodem) receiveBlock(header byte, useCRC bool, expectedSeq byte) (data []byte, isDuplicate bool, err error) {
	// Determine block size from header
	var blockSize int
	switch header {
	case SOH:
		blockSize = 128
	case STX:
		blockSize = 1024
	default:
		return nil, false, fmt.Errorf("unexpected header byte 0x%02x", header)
	}

	// Read sequence number and complement
	seqBuf := make([]byte, 2)
	if err := x.readFull(seqBuf); err != nil {
		return nil, false, fmt.Errorf("reading sequence: %w", err)
	}

	seq := seqBuf[0]
	seqComp := seqBuf[1]

	// Verify complement
	if seq+seqComp != 255 {
		return nil, false, fmt.Errorf("%w: seq=%d comp=%d", ErrSequenceMismatch, seq, seqComp)
	}

	// Read data block
	data = make([]byte, blockSize)
	if err := x.readFull(data); err != nil {
		return nil, false, fmt.Errorf("reading data: %w", err)
	}

	// Read and verify checksum/CRC
	if useCRC {
		crcBuf := make([]byte, 2)
		if err := x.readFull(crcBuf); err != nil {
			return nil, false, fmt.Errorf("reading CRC: %w", err)
		}
		receivedCRC := uint16(crcBuf[0])<<8 | uint16(crcBuf[1])
		computedCRC := crc16.CRC(data, 0)
		if receivedCRC != computedCRC {
			return nil, false, fmt.Errorf("%w: received 0x%04x, computed 0x%04x", ErrChecksumMismatch, receivedCRC, computedCRC)
		}
	} else {
		csBuf := make([]byte, 1)
		if err := x.readFull(csBuf); err != nil {
			return nil, false, fmt.Errorf("reading checksum: %w", err)
		}
		if csBuf[0] != checksum8(data) {
			return nil, false, fmt.Errorf("%w: received 0x%02x, computed 0x%02x", ErrChecksumMismatch, csBuf[0], checksum8(data))
		}
	}

	// Check sequence number
	if seq != expectedSeq {
		// Duplicate block (previous sequence) — ACK but don't write
		prevSeq := expectedSeq - 1
		if expectedSeq == 0 {
			prevSeq = 255
		}
		if seq == prevSeq {
			log.Debugf("receive: duplicate block %d, ACK without writing", seq)
			return nil, true, nil
		}
		return nil, false, fmt.Errorf("%w: expected %d, got %d", ErrSequenceMismatch, expectedSeq, seq)
	}

	return data, false, nil
}

// Receive accepts an incoming XMODEM transfer and writes the received data to w.
// The receiver initiates by sending 'C' (for CRC/1K modes) or NAK (for checksum
// mode) to the sender. Blocks are verified and acknowledged until the sender
// signals end of transmission with EOT.
//
// Note: the received data may include trailing padding bytes (default SUB/0x1A)
// that the sender used to fill the last block. The caller is responsible for
// stripping padding if the original file size is known.
func (x Xmodem) Receive(w io.Writer) error {
	var (
		errorCount         = 0
		expectedSeq   byte = 1
		totalReceived      = 0
		bytePacket         = make([]byte, 1)
		useCRC             = x.Mode != XMode128
		handshakeDone      = false
	)

	x.port.Flush()

	// Send initial handshake byte to tell sender we're ready
	for !handshakeDone && errorCount <= x.retries {
		var initByte byte
		if useCRC {
			initByte = CRC
		} else {
			initByte = NAK
		}
		log.Tracef("receive: sending handshake byte 0x%02x", initByte)
		if _, err := x.port.Write([]byte{initByte}); err != nil {
			log.Errorf("Error writing handshake: %v", err)
			errorCount++
			continue
		}

		if _, err := x.port.Read(bytePacket); err != nil {
			log.Errorf("Timeout waiting for sender: %v", err)
			errorCount++
			continue
		}

		switch bytePacket[0] {
		case SOH, STX, EOT:
			handshakeDone = true
		case CAN:
			log.Errorf("Sender canceled transfer")
			return ErrTransferCanceled
		default:
			log.Debugf("Unexpected byte during handshake: 0x%02x", bytePacket[0])
			errorCount++
		}
	}
	if !handshakeDone {
		log.Errorf("receive: too many handshake errors, aborting")
		return ErrTransferCanceled
	}

	// Main receive loop
	for {
		header := bytePacket[0]

		// Handle EOT
		if header == EOT {
			log.Info("receive: EOT received, sending ACK")
			x.port.Write([]byte{ACK})
			log.Printf("receive: transfer complete, %d blocks received", totalReceived)
			return nil
		}

		// Handle CAN
		if header == CAN {
			if _, err := x.port.Read(bytePacket); err == nil && bytePacket[0] == CAN {
				log.Errorf("receive: double CAN, transfer canceled")
				return ErrTransferCanceled
			}
			log.Debugf("receive: single CAN, treating as error")
			errorCount++
			if errorCount > x.retries {
				return ErrTransferCanceled
			}
			x.port.Write([]byte{NAK})
		} else if header == SOH || header == STX {
			data, isDuplicate, err := x.receiveBlock(header, useCRC, expectedSeq)
			if err != nil {
				log.Errorf("receive: block error: %v", err)
				errorCount++
				if errorCount > x.retries {
					x.Abort()
					return err
				}
				x.port.Write([]byte{NAK})
			} else if isDuplicate {
				x.port.Write([]byte{ACK})
			} else {
				if _, err := w.Write(data); err != nil {
					log.Errorf("Error writing received data: %v", err)
					x.Abort()
					return err
				}
				errorCount = 0
				expectedSeq = byte((int(expectedSeq) + 1) % 256)
				totalReceived++
				log.Tracef("receive: block %d ACK'd", totalReceived)
				x.port.Write([]byte{ACK})
			}
		} else {
			log.Errorf("receive: unexpected header byte 0x%02x", header)
			errorCount++
			if errorCount > x.retries {
				x.Abort()
				return ErrTransferCanceled
			}
			x.port.Write([]byte{NAK})
		}

		// Read next header byte
		if _, err := x.port.Read(bytePacket); err != nil {
			log.Errorf("Error reading next header: %v", err)
			errorCount++
			if errorCount > x.retries {
				x.Abort()
				return err
			}
		}
	}
}
