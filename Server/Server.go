package main

//jsd

import (
	"encoding/binary"
	"io"
	"io/ioutil"
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

var ptInfo pt.ServerInfo

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

func copyLoop(conn, or net.Conn) {
	var queue = make(chan []byte, 2)

	var wg sync.WaitGroup
	wg.Add(3)

	go func() {
		for {
			buffer := make([]byte, data_size)
			n, err := or.Read(buffer)
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
					conn.Write(data)
				default:
					buffer := make([]byte, data_size+header_size)
					conn.Write(buffer)
				}
			} else {
				buffer := make([]byte, data_size+header_size)
				conn.Write(buffer)
			}
		}
		wg.Done()
	}()

	go func() {
		ch := chunkedreader.New(conn, header_size+data_size)
		for ch.Read() {
			ms := ch.Bytes()

			lD_a := ms[0:header_size]
			lD := binary.BigEndian.Uint64(lD_a)
			if lD > 0 {
				or.Write(ms[header_size : lD+header_size])
			}
		}
		wg.Done()
	}()

	wg.Wait()
}

func handler(conn net.Conn) error {
	defer conn.Close()

	handlerChan <- 1
	defer func() {
		handlerChan <- -1
	}()

	or, err := pt.DialOr(&ptInfo, conn.RemoteAddr().String(), "dummy")
	if err != nil {
		return err
	}
	defer or.Close()

	copyLoop(conn, or)

	return nil
}

func acceptLoop(ln net.Listener) error {
	defer ln.Close()
	for {
		conn, err := ln.Accept()
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

	ptInfo, err = pt.ServerSetup(nil)
	if err != nil {
		os.Exit(1)
	}

	listeners := make([]net.Listener, 0)
	for _, bindaddr := range ptInfo.Bindaddrs {
		switch bindaddr.MethodName {
		case "dummy":
			ln, err := net.ListenTCP("tcp", bindaddr.Addr)
			if err != nil {
				pt.SmethodError(bindaddr.MethodName, err.Error())
				break
			}
			go acceptLoop(ln)
			pt.Smethod(bindaddr.MethodName, ln.Addr())
			listeners = append(listeners, ln)
		default:
			pt.SmethodError(bindaddr.MethodName, "no such method")
		}
	}
	pt.SmethodsDone()

	var numHandlers int = 0
	var sig os.Signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGTERM)

	if os.Getenv("TOR_PT_EXIT_ON_STDIN_CLOSE") == "1" {
		go func() {
			io.Copy(ioutil.Discard, os.Stdin)
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
