package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"duanjuapp/native/core"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "用法：moduletool <模块目录> <请求 JSON> [<请求 JSON> ...]")
		os.Exit(2)
	}
	data, err := os.MkdirTemp("", "moduletool-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(data)
	module, err := filepath.Abs(os.Args[1])
	if err != nil {
		panic(err)
	}
	if err := os.CopyFS(filepath.Join(data, "modules", filepath.Base(module)), os.DirFS(module)); err != nil {
		panic(err)
	}
	initialize, _ := json.Marshal(map[string]string{"action": "initialize", "directory": data, "cache": filepath.Join(data, "cache")})
	fmt.Println(core.NativeRequest(string(initialize)))
	for _, request := range os.Args[2:] {
		fmt.Println(core.NativeRequest(request))
	}
	if log, err := os.ReadFile(filepath.Join(data, "logs", "app.log")); err == nil {
		fmt.Fprintln(os.Stderr, string(log))
	}
}
