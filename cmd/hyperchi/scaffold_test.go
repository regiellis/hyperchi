package main

import (
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGeneratedProjectsBuild(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"site", "full"} {
		t.Run(kind, func(t *testing.T) {
			t.Chdir(t.TempDir())
			if err := createProject("sample", root, kind); err != nil {
				t.Fatal(err)
			}
			if err := createProject("sample", root, kind); err == nil {
				t.Fatal("existing project overwritten")
			}
			if kind == "site" {
				if _, err := os.Stat("sample/package.json"); !os.IsNotExist(err) {
					t.Fatal("site scaffold includes Node dependencies")
				}
			}
			source, err := os.ReadFile("sample/main.go")
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(source), "http.ListenAndServe(") {
				t.Fatal("scaffold bypasses server lifecycle")
			}
			binary := filepath.Join(t.TempDir(), "app")
			cmd := exec.Command("go", "build", "-o", binary, ".")
			cmd.Dir = "sample"
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("generated %s: %v\n%s", kind, err, output)
			}
			testGeneratedServer(t, binary, "sample", kind)
		})
	}
}

func testGeneratedServer(t *testing.T, binary, dir, kind string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	logs, err := os.Create(filepath.Join(t.TempDir(), "server.log"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := logs.Close(); err != nil {
			t.Errorf("close server log: %v", err)
		}
	})
	process := exec.Command(binary)
	process.Dir = dir
	process.Env = append(os.Environ(), "HOST=127.0.0.1", "PORT="+port, "ENVIRONMENT=production")
	process.Stdout = logs
	process.Stderr = logs
	if err := process.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := process.Process.Signal(os.Interrupt); err != nil {
			t.Error(err)
		}
		done := make(chan error, 1)
		go func() { done <- process.Wait() }()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("server exit: %v", err)
			}
		case <-time.After(5 * time.Second):
			if err := process.Process.Kill(); err != nil {
				t.Errorf("kill server: %v", err)
			}
			<-done
			t.Error("server failed graceful shutdown")
		}
	}()
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(10 * time.Second)
	for {
		res, err := client.Get("http://" + addr + "/")
		if err == nil {
			if cerr := res.Body.Close(); cerr != nil {
				t.Fatal(cerr)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("generated server did not start")
		}
		time.Sleep(20 * time.Millisecond)
	}
	paths := []string{"/"}
	if kind == "site" {
		paths = append(paths, "/healthz", "/static/site.css")
	}
	for _, path := range paths {
		for _, method := range []string{"GET", "HEAD"} {
			req, err := http.NewRequest(method, "http://"+addr+path, nil)
			if err != nil {
				t.Fatal(err)
			}
			res, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			body, readErr := io.ReadAll(res.Body)
			if cerr := res.Body.Close(); cerr != nil {
				t.Fatal(cerr)
			}
			if readErr != nil {
				t.Fatal(readErr)
			}
			if res.StatusCode != 200 {
				t.Errorf("%s %s: %d", method, path, res.StatusCode)
			}
			if method == "HEAD" && len(body) != 0 {
				t.Error("HEAD returned body")
			}
			if res.Header.Get("Content-Security-Policy") == "" {
				t.Error("production scaffold lacks CSP")
			}
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "hyperchi.db")); !os.IsNotExist(err) {
		t.Error("scaffold created an unwanted database")
	}
}
