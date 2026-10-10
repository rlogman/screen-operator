package rfb

import (
	"crypto/des"
	"fmt"
	"strings"
)

// VNCAuthResponse cifra el desafio de 16 bytes de VNC Authentication (RFC 6143,
// 7.2.2) con DES-ECB. La clave son los primeros 8 bytes de la contrasena,
// rellenados con ceros, con los bits de cada byte invertidos (convencion
// historica de VNC: el bit menos significativo va primero).
func VNCAuthResponse(challenge []byte, password string) ([]byte, error) {
	if len(challenge) != 16 {
		return nil, fmt.Errorf("rfb: el desafio VNC debe tener 16 bytes, tiene %d", len(challenge))
	}
	var key [8]byte
	copy(key[:], password)
	for i, b := range key {
		key[i] = reverseBits(b)
	}
	block, err := des.NewCipher(key[:])
	if err != nil {
		return nil, fmt.Errorf("rfb: no se pudo preparar DES: %v", err)
	}
	out := make([]byte, 16)
	block.Encrypt(out[:8], challenge[:8])
	block.Encrypt(out[8:], challenge[8:])
	return out, nil
}

func reverseBits(b byte) byte {
	var r byte
	for i := 0; i < 8; i++ {
		r = r<<1 | b&1
		b >>= 1
	}
	return r
}

// redact reemplaza cada aparicion del secreto en msg. Defensa en profundidad:
// los mensajes de error se construyen sin el secreto, pero errores de terceros
// (dial HTTP, websocket) podrian incluir la URL completa.
func redact(msg, secret string) string {
	if secret == "" {
		return msg
	}
	return strings.ReplaceAll(msg, secret, "[redactado]")
}
