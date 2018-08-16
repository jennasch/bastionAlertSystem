package ssh

import (
	"github.com/notion/trove_ssh_bastion/config"
	"crypto/rsa"
	"crypto/rand"
	"github.com/fatih/color"
	"log"
	"encoding/pem"
	"crypto/x509"
	"encoding/binary"
	"syscall"
	"unsafe"
)

func createPrivateKey(env *config.Env) []byte {
	pk, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		color.Set(color.FgRed)
		log.Fatal(err)
		color.Unset()
	}

	color.Set(color.FgBlue)
	log.Println("Generated RSA Keypair")
	color.Unset()

	pemdata := pem.EncodeToMemory(
		&pem.Block{
			Type: "RSA PRIVATE KEY",
			Bytes: x509.MarshalPKCS1PrivateKey(pk),
		},
	)

	bytes := []byte(pemdata)

	env.Config.PrivateKey = bytes

	return bytes
}


func parseDims(b []byte) (uint32, uint32) {
	w := binary.BigEndian.Uint32(b)
	h := binary.BigEndian.Uint32(b[4:])
	return w, h
}

type Winsize struct {
	Height uint16
	Width  uint16
	x      uint16 // unused
	y      uint16 // unused
}

// SetWinsize sets the size of the given pty.
func SetWinsize(fd uintptr, w, h uint32) {
	ws := &Winsize{Width: uint16(w), Height: uint16(h)}
	syscall.Syscall(syscall.SYS_IOCTL, fd, uintptr(syscall.TIOCSWINSZ), uintptr(unsafe.Pointer(ws)))
}