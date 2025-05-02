# XMODEM

A robust XMODEM implementation in Go that supports XMODEM-CRC and XMODEM-1K protocols.
This library was created to address limitations in existing Go XMODEM implementations, particularly around XMODEM-CRC support.

## Features

- Supports XMODEM-CRC and XMODEM-1K protocols
- Configurable retries and timeouts
- Automatic protocol detection
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

### Advanced Configuration

```go
// Create with existing serial port
port, _ := serial.OpenPort(&serial.Config{
    Name: "/dev/ttyUSB0",
    Baud: 115200,
})
x := xmodem.NewWithPort(port)

// Configure settings
x.Mode = xmodem.XMode1K  // Use XMODEM-1K
x.Padding = 0x1A         // Set padding character
x.retries = 5            // Set retry count
x.Timeout = time.Second * 10  // Set timeout
```

## Protocol Support

- XMODEM-CRC (default)
- XMODEM-1K
- XMODEM-128 (checksum mode not implemented)

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
