package xmodem

import (
	"bytes"
	"errors"
	"time"

	"github.com/taigrr/log-socket/log"
	"github.com/tarm/serial"

	"github.com/taigrr/xmodem/crc16"
)

const (
	SOH = 0x01
	STX = 0x02
	EOT = 0x04
	ACK = 0x06
	DLE = 0x10
	NAK = 0x15
	CAN = 0x18
	SUB = 0x1A
	CRC = 'C'

	XMode128 Mode = iota
	XModeCRC
	XMode1K
)

type (
	Mode   int
	Xmodem struct {
		port    *serial.Port
		Padding byte
		Mode    Mode
		retries int
		Timeout time.Duration
	}
)

var ErrTransferCanceled = errors.New("transfer canceled")

func (x Xmodem) Abort() {
	x.port.Write([]byte{CAN, CAN})
}

func NewWithPort(port *serial.Port) *Xmodem {
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

func (x Xmodem) Send(payload bytes.Buffer) error {
	var (
		errorCount = 0
		cancel     = 0
		bytePacket = make([]byte, 1)
		totalSent  = 0
	)

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
			// TODO: Implement CRC8
			return errors.New("128 mode checksum not implemented")
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
			_, err := x.port.Write(header)
			if err != nil {
				log.Errorf("Error writing header: %v", err)
				errorCount++
				if errorCount > x.retries {
					log.Errorf("send error: error_count reached %d, aborting.\n", x.retries)
					return ErrTransferCanceled
				}
				continue
			}
			_, err = x.port.Write(data)
			if err != nil {
				log.Errorf("Error writing data: %v", err)
				errorCount++
				if errorCount > x.retries {
					log.Errorf("send error: error_count reached %d, aborting.\n", x.retries)
					return ErrTransferCanceled
				}
				continue
			}
			_, err = x.port.Write(checkSum)
			if err != nil {
				log.Errorf("Error writing checksum: %v", err)
				errorCount++
				if errorCount > x.retries {
					log.Errorf("send error: error_count reached %d, aborting.\n", x.retries)
					return ErrTransferCanceled
				}
				continue
			}
			// Listen for first NAK or CRC
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
