# kcp2k-go

a golang implementation of [Mirror KCP](https://github.com/MirrorNetworking/kcp2k)
开箱即用，可直接和Mirror kcp通信

## 说明
mirror游戏框架自带的kcp是在kcp协议底层基础上自定义了一套通信协议<br>
主要特性对比：

|            | 传输特性       | 握手和关闭|
|------------|------------|------------|
| Mirror KCP | 支持可靠和非可靠传输 | 支持|
| KCP        | 支持可靠传输     | 不支持|

## Examples
[simple example](./example/simple/main.go)

## 编码
在原传输包文基础上增加了kcp2kHeader,以支持区分可靠传输和非可靠传输

| 1字节     | 4字节    |
 |---------|--------|
| channel | cookie | 

channel:
* 1 可靠传输
* 2 非可靠传输

cookie是连接建立后的“凭据”，官方说法是的作用是：
```
generate a random cookie for this connection to avoid UDP spoofing
```

### 可靠传输编码
mirror kcp支持握手和关闭，传输data的第一个字节为控制位
```go
type Kcp2kOpcode byte

const (
	Hello      Kcp2kOpcode = 1
	Ping       Kcp2kOpcode = 2
	Data       Kcp2kOpcode = 3
	Disconnect Kcp2kOpcode = 4
)
```
最终udp发送出去的原始编码为:

| 1字节  | 4字节    | 24字节         | 1字节| N字节|
|------|--------|--------------|-------|-------|
| 0x01 | cookie | kcp protocol |Kcp2kOpcode|data|


### 非可靠传输编码
非可靠传输编码比较简单，如下：

| 1字节  | 4字节    |  N字节|
|------|--------|-------|
| 0x02 | cookie | data|

## Thanks
需要kcp-go(https://github.com/xtaci/kcp-go)的UDPSession暴露出读kcp整一条数据的接口<br>
所以就fork修改了一下: https://github.com/0990/kcp-go
