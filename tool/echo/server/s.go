package main

import (
	"fmt"
	"net"
	"os"
	"strings"
)

func main() {
	// Listen for incoming connections.
	l, err := net.Listen("tcp", fmt.Sprintf(":%d", 3333))
	if err != nil {
		fmt.Println("Error listening:", err.Error())
		os.Exit(1)
	}
	// Close the listener when the application closes.
	defer l.Close()
	for {
		// Listen for an incoming connection.
		conn, err := l.Accept()
		if err != nil {
			fmt.Println("Error accepting: ", err.Error())
			os.Exit(1)
		}
		// Handle connections in a new goroutine.
		go handleRequest(conn)
	}
}

// Handles incoming requests.
func handleRequest(conn net.Conn) {
	buf := make([]byte, 1024)
	_, err := conn.Read(buf)
	defer conn.Close()
	if err != nil {
		fmt.Println("Error reading:", err.Error())
	} else {
		split := strings.Split(string(buf), "\n")
		for _, s := range split {
			fmt.Println(s)
			conn.Write([]byte(s))
		}
	}
}
