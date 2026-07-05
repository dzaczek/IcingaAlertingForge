package main

import (
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	dir, _ := os.MkdirTemp("", "test")
	defer os.RemoveAll(dir)

	file := filepath.Join(dir, "test.log")
	os.WriteFile(file, []byte("test"), 0644)

	os.Chmod(dir, 0500)

	f, err := os.Open(file)
	fmt.Println("Open:", err)
	if f != nil {
		f.Close()
	}

	_, err = os.CreateTemp(dir, "tmp-*")
	fmt.Println("CreateTemp:", err)
}
