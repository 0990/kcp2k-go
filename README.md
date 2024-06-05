# kcp2k-go
[中文文档](doc/README_zh.md)<br>
A Golang implementation of Mirror kcp ([kcp2k](https://github.com/MirrorNetworking/kcp2k))<br>
Ready to use, can communicate directly with Mirror kcp

## Introduction
The kcp2k included with the Mirror game framework customizes a communication protocol based on the underlying kcp protocol<br>
Main feature comparison:

|       | Transmission Features | Handshake and Close |
|-------|----------------------|---------------------|
| KCP2K | Supports reliable and unreliable transmission | Supported |
| KCP   | Supports reliable transmission only     | Not Supported |

## Examples
[simple example](./example/simple/main.go)

## kcp2k Encoding
Adds a kcp2kHeader on top of the original transmission packet to support distinguishing between reliable and unreliable transmissions

| 1 byte  | 4 bytes  |
|---------|----------|
| channel | cookie   | 

channel:
* 1 Reliable Transmission
* 2 Unreliable Transmission

The cookie is a "credential" after the connection is established, and its official purpose is:
```
generate a random cookie for this connection to avoid UDP spoofing
```

### Reliable Transmission Encoding
Mirror kcp supports handshake and close, with the first byte of the transmitted data being the control bit
```go
type Kcp2kOpcode byte

const (
    Hello      Kcp2kOpcode = 1
    Ping       Kcp2kOpcode = 2
    Data       Kcp2kOpcode = 3
    Disconnect Kcp2kOpcode = 4
)
```
The final raw encoding sent via udp is:

| 1 byte  | 4 bytes  | 24 bytes        | 1 byte       | N bytes |
|---------|----------|-----------------|--------------|---------|
| 0x01    | cookie   | kcp protocol    | Kcp2kOpcode  | data    |

### Unreliable Transmission Encoding
Unreliable transmission encoding is simpler, as follows:

| 1 byte  | 4 bytes  | N bytes |
|---------|----------|---------|
| 0x02    | cookie   | data    |

## Additional Information
[kcp-go](https://github.com/xtaci/kcp-go) need  exposing the interface to read a whole kcp data packet from the UDPSession<br>
So, I forked and modified it: https://github.com/0990/kcp-go
