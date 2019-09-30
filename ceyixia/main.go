package main

import (
	"fmt"
	"k8s.io/apimachinery/pkg/util/wait"
	"time"
)

func test() {
	for true {
		fmt.Println("Hello " + time.Now().String())
		time.Sleep(3 * time.Second)
	}
}

func main() {
	period := 1 * time.Second
	wait.Forever(test, period)
}
