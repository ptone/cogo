package main

import (
	"fmt"

	"example.com/smoke/gate"
	"example.com/smoke/service"
)

func main() {
	g := gate.New(true)
	r := service.NewRunner(g)
	if err := r.RunBash("echo hi"); err != nil {
		fmt.Println("bash:", err)
	}
	if err := g.CheckBash("ls"); err != nil {
		fmt.Println("direct bash:", err)
	}
}
