package main

import (
    "fmt"
    "os"
)

func main() {
    f, _ := os.CreateTemp("", "test")
    fmt.Println(f.Close())
    fmt.Println(f.Close())
}
