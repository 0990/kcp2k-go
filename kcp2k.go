package kcp2k

import "errors"

type Kcp2kOpcode byte

const (
	Hello      Kcp2kOpcode = 1
	Ping       Kcp2kOpcode = 2
	Data       Kcp2kOpcode = 3
	Disconnect Kcp2kOpcode = 4
)

func parseKcp2kBodyData(rawData []byte) (opcode Kcp2kOpcode, data []byte, err error) {
	if len(rawData) < 1 {
		return 0, nil, errors.New("invalid kcp2k data")
	}

	opcode = Kcp2kOpcode(rawData[0])
	data = rawData[1:]

	return opcode, data, nil
}
