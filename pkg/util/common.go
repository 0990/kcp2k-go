package util

import (
	"hash/fnv"
	"net"
)

func ConnectionHash(endPoint *net.UDPAddr) uint32 {
	h := fnv.New32a()
	h.Write([]byte(endPoint.String()))
	return h.Sum32()
}
