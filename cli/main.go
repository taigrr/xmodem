package main

import (
	"bytes"
	"os"

	"github.com/taigrr/log-socket/log"
	"github.com/taigrr/xmodem"
)

func main() {
	// path, file
	modem, err := xmodem.New(os.Args[1], 9600)
	if err != nil {
		panic(err)
	}
	modem.Mode = xmodem.XMode1K
	f, err := os.ReadFile(os.Args[2])
	if err != nil {
		panic(err)
	}
	b := bytes.NewBuffer(f)
	log.Infof("Sending file: %s", os.Args[2])
	if err := modem.Send(*b); err != nil {
		log.Fatalf("Send failed: %v", err)
	}
	log.Info("File sent")

	log.Flush()
}
