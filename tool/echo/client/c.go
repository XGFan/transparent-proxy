package main

import (
	"fmt"
	"log"
	"net"
)

func main() {
	dial, err := net.Dial("tcp", "localhost:3333")
	if err != nil {
		log.Fatalln(err)
	}
	dial.Write([]byte("hello"))
	bytes := make([]byte, 1024)
	dial.Read(bytes)
	fmt.Println(string(bytes))
}
