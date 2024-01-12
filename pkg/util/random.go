package util

import "crypto/rand"

func RandBytes(n int) []byte {
	var b []byte = make([]byte, n)
	rand.Read(b)
	return b
}
