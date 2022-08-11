package main

import (
	"fmt"
	"github.com/lin168/godet"
	"time"
)

func main() {
	debugger, err := godet.StartCapture("localhost:9222", false)
	if err != nil {
		panic(err)
	}

	version, err := debugger.Version()
	if err != nil {
		panic(err)
	}

	fmt.Println(version)

	_ = godet.AddDebugger("5B4D8C2604BE25E40BC31EC1D67ED26B", "ws://localhost:9222/devtools/page/")

	godet.AddEventListener("Network.requestWillBeSent", func(params godet.Params) {
		fmt.Println(params)
	})

	time.Sleep(10 * time.Second)
	fmt.Println(time.Now())
	godet.StopCapture()
	fmt.Println(time.Now())
}
