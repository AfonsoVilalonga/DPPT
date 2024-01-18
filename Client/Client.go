package main

import (
	"encoding/binary"
	"io"
	"math/rand"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/knadh/chunkedreader"
	pt "gitlab.torproject.org/tpo/anti-censorship/pluggable-transports/goptlib"
)

var ptInfo pt.ClientInfo

var handlerChan = make(chan int)

// PARAMETERS
const time_value = 1
const data_size = 1024
const time_metric = time.Microsecond

const header_size = 8

func randomizeResp() bool {
	rand.Seed(time.Now().Unix())
	result := rand.Intn(6)
	send := false

	if result == 0 {
		send = true
	} else if result == 5 {
		send = false
	} else {
		result := rand.Intn(2)
		if result == 0 {
			send = true
		} else {
			send = false
		}
	}

	return send
}

func copyLoop(conn, remote net.Conn) {
	var queue = make(chan []byte, 1000)

	var wg sync.WaitGroup
	wg.Add(3)

	go func() {
		for {
			buffer := make([]byte, data_size)
			n, err := conn.Read(buffer)
			if err != nil {
				break
			}

			bytes := make([]byte, 8)
			binary.BigEndian.PutUint64(bytes, uint64(n))
			buffer = append(bytes, buffer...)

			queue <- buffer
		}
		wg.Done()
	}()

	go func() {
	ForLoop:
		for {
			time.Sleep(time_value * time_metric)

			if randomizeResp() {
				select {
				case data, ok := <-queue:
					if !ok {
						break ForLoop
					}
					remote.Write(data)
				default:
					buffer := make([]byte, data_size+header_size)
					remote.Write(buffer)
				}
			} else {
				buffer := make([]byte, data_size+header_size)
				remote.Write(buffer)
			}

		}
		wg.Done()
	}()

	go func() {
		ch := chunkedreader.New(remote, header_size+data_size)
		for ch.Read() {
			ms := ch.Bytes()

			lD_a := ms[0:header_size]
			lD := binary.BigEndian.Uint64(lD_a)
			if lD > 0 {
				conn.Write(ms[header_size : lD+header_size])
			}
		}
		wg.Done()
	}()

	wg.Wait()
}

func handler(conn *pt.SocksConn) error {
	handlerChan <- 1
	defer func() {
		handlerChan <- -1
	}()

	defer conn.Close()
	remote, err := net.Dial("tcp", conn.Req.Target)
	if err != nil {
		conn.Reject()
		return err
	}
	defer remote.Close()
	err = conn.Grant(remote.RemoteAddr().(*net.TCPAddr))
	if err != nil {
		return err
	}

	copyLoop(conn, remote)

	return nil
}

func acceptLoop(ln *pt.SocksListener) error {
	defer ln.Close()
	for {
		conn, err := ln.AcceptSocks()
		if err != nil {
			if e, ok := err.(net.Error); ok && e.Temporary() {
				continue
			}
			return err
		}
		go handler(conn)
	}
}

func main() {
	var err error

	ptInfo, err = pt.ClientSetup(nil)
	if err != nil {
		os.Exit(1)
	}

	if ptInfo.ProxyURL != nil {
		pt.ProxyError("proxy is not supported")
		os.Exit(1)
	}

	listeners := make([]net.Listener, 0)
	for _, methodName := range ptInfo.MethodNames {
		switch methodName {
		case "dummy":
			ln, err := pt.ListenSocks("tcp", "127.0.0.1:0")
			if err != nil {
				pt.CmethodError(methodName, err.Error())
				break
			}
			go acceptLoop(ln)
			pt.Cmethod(methodName, ln.Version(), ln.Addr())
			listeners = append(listeners, ln)
		default:
			pt.CmethodError(methodName, "no such method")
		}
	}
	pt.CmethodsDone()

	var numHandlers int = 0
	var sig os.Signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGTERM)

	if os.Getenv("TOR_PT_EXIT_ON_STDIN_CLOSE") == "1" {
		go func() {
			io.Copy(io.Discard, os.Stdin)
			sigChan <- syscall.SIGTERM
		}()
	}

	sig = nil
	for sig == nil {
		select {
		case n := <-handlerChan:
			numHandlers += n
		case sig = <-sigChan:
		}
	}

	for _, ln := range listeners {
		ln.Close()
	}
	for numHandlers > 0 {
		numHandlers += <-handlerChan
	}
}
