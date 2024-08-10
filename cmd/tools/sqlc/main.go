package main

import (
	"log"
	"os/exec"
)

func main() {
	cmd := exec.Command(
		"sqlc",
		"generate",
		"-f",
		"./internal/store/pgstore/sqlc.yaml",
	)
	if err := cmd.Run(); err != nil {
		log.Fatal("Error running sqlc: ", err)
		panic(err)
	}
	log.Println("sqlc generated successfully")
}
