package main

import (
	"fmt"
	"github.com/0990/kcp2k-go"
	"log"
	"time"
)

func main() {
	if listener, err := kcp2k.ListenWithOptions("127.0.0.1:12345"); err == nil {
		// spin-up the client
		//go clientSendUnreliable()
		for {
			s, err := listener.Accept()
			if err != nil {
				log.Fatal(err)
			}
			go handleEcho(s)
		}
	} else {
		log.Fatal(err)
	}
}

// handleEcho send back everything it received
func handleEcho(conn *kcp2k.Session) {
	fmt.Println("new session", conn.RemoteAddr())
	buf := make([]byte, 100)
	for {
		n, channel, err := conn.Read(buf)
		if err != nil {
			log.Println(err)
			return
		}

		log.Println("server recv:", string(buf[:n]), channel)

		n, err = conn.Send(buf[:n], channel)
		if err != nil {
			log.Println(err)
			return
		}
	}
}

func clientSendReliable() {
	// wait for server to become ready
	time.Sleep(time.Second)

	// dial to the echo server
	if sess, err := kcp2k.DialWithOptions("127.0.0.1:12345"); err == nil {
		for {
			data := time.Now().String()
			buf := make([]byte, len(data))
			log.Println("sent reliable:", data)
			if _, err := sess.Send([]byte(data), kcp2k.Reliable); err == nil {
				if err != nil {
					log.Println(err)
					return
				}
				// read back the data
				if n, reliable, err := sess.Read(buf); err == nil {
					log.Println("recv reliable:", string(buf[:n]), reliable)
				} else {
					log.Fatal(err)
				}
			} else {
				log.Fatal(err)
			}
			time.Sleep(time.Second)
		}
	} else {
		log.Fatal(err)
	}
}

func clientSendUnreliable() {
	// wait for server to become ready
	time.Sleep(time.Second)

	// dial to the echo server
	if sess, err := kcp2k.DialWithOptions("127.0.0.1:12345"); err == nil {
		for {
			data := time.Now().String()
			buf := make([]byte, len(data))
			log.Println("sent unreliable:", data)
			sess.Send([]byte(data), kcp2k.Unreliable)

			// read back the data
			if n, reliable, err := sess.Read(buf); err == nil {
				log.Println("recv unreliable:", string(buf[:n]), reliable)
			} else {
				log.Fatal(err)
			}
			time.Sleep(time.Second)
		}
	} else {
		log.Fatal(err)
	}
}
