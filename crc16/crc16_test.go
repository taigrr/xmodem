package crc16

import "testing"

func TestCRCString(t *testing.T) {
	crc := CRCString("helloworld", 0)
	if crc != 0x4ab3 {
		t.Errorf("CRCString(\"helloworld\") = %x, want 0x4ab3", crc)
	}
	crc = CRCString("123456789", 0)
	if crc != 0x31c3 {
		t.Errorf("CRCString(\"123456789\") = %x, want 0x31c3", crc)
	}
}

func TestCRC(t *testing.T) {
	crc := CRC([]byte{0x00, 0x01, 0x02, 0x03, 0x04}, 0)
	if crc != 0x0d03 {
		t.Errorf("CRC(01234) = 0x%x, want 0x0d03", crc)
	}
}
