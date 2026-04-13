# XMODEM

A robust XMODEM implementation in Go that supports XMODEM-CRC and XMODEM-1K protocols.
This library was created to address limitations in existing Go XMODEM implementations, particularly around XMODEM-CRC support.

## Features

- **Send and Receive** — full bidirectional XMODEM transfers
- Supports XMODEM-128 (checksum), XMODEM-CRC, and XMODEM-1K protocols
- Configurable retries and timeouts
- Automatic protocol detection
- `Port` interface for testing and non-serial transports
- Simple API for file transfers

## Usage

### Basic Example

```go
package main

import (
    "bytes"
    "github.com/taigrr/xmodem"
    "github.com/tarm/serial"
)

func main() {
    // Create a new XMODEM instance with a serial port
    x, err := xmodem.New("/dev/ttyUSB0", 115200)
    if err != nil {
        panic(err)
    }
    defer x.port.Close()

    // Prepare your data
    data := []byte("Hello, XMODEM!")
    buffer := bytes.NewBuffer(data)

    // Send the data
    err = x.Send(*buffer)
    if err != nil {
        panic(err)
    }
}
```

### Receiving a File

```go
package main

import (
    "os"
    "github.com/taigrr/xmodem"
)

func main() {
    x, err := xmodem.New("/dev/ttyUSB0", 115200)
    if err != nil {
        panic(err)
    }

    f, err := os.Create("received.bin")
    if err != nil {
        panic(err)
    }
    defer f.Close()

    // Receive initiates the handshake and writes blocks to the writer
    if err := x.Receive(f); err != nil {
        panic(err)
    }
}
```

### Using the Port Interface (Testing / Non-Serial)

```go
// Any type implementing io.ReadWriter + Flush() works
x := xmodem.NewWithReadWriter(myPort)
x.Mode = xmodem.XMode1K
```

## Protocol Support

- **XMODEM-CRC** (default) — 128-byte blocks with 16-bit CRC
- **XMODEM-1K** — 1024-byte blocks with 16-bit CRC
- **XMODEM-128** — 128-byte blocks with 8-bit checksum

## References

- [tarm](https://github.com/tarm/serial/blob/master/serial_linux.go)
- [c implementation](https://github.com/kelvinlawson/xmodem-1k/blob/master/xmodem.c#L133)
- [protocol writeup](https://www.adontec.com/xmodem-protocol.htm)
- [nindepedia](https://www.ninerpedia.org/wiki/Protocols)
- [wikipedia](https://en.wikipedia.org/wiki/XMODEM#XMODEM-1K)
- [testing fork](https://github.com/taigrr/go-xmodem)

## Disclaimer

The CRC table implementation in this library is not 0BSD licensed. It is used under fair use for educational and compatibility purposes.
Please refer to the original source for licensing details.
