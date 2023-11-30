# XMODEM

After repeated struggles with non-compliant, not-working golang xmodem libraries,
here is my own which works with the hardware I use.

It should more closely follow the XMODEM spec, but it will at least work for
XMODEM-CRC, which is notably missing from other golang implementations, which
often only work for XMODEM-1K.

Here are the referenced codebases and documents:

- [tarm](https://github.com/tarm/serial/blob/master/serial_linux.go)
- [c implementation](https://github.com/kelvinlawson/xmodem-1k/blob/master/xmodem.c#L133)
- [protocol writeup](https://www.adontec.com/xmodem-protocol.htm)
- [nindepedia](https://www.ninerpedia.org/wiki/Protocols)
- [wikipedia](https://en.wikipedia.org/wiki/XMODEM#XMODEM-1K)
- [testing fork](https://github.com/taigrr/go-xmodem)
