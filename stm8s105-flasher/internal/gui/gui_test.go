package gui

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestPageAndIdentifyWithoutPort(t *testing.T) {
	srv := httptest.NewServer(handler())
	defer srv.Close()

	page, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(page.Body)
	page.Body.Close()
	if page.StatusCode != 200 || !strings.Contains(string(body), "串口烧录") || !strings.Contains(string(body), "PD5") {
		t.Fatalf("page status %d body %s", page.StatusCode, body)
	}

	ports, err := http.Get(srv.URL + "/api/ports")
	if err != nil {
		t.Fatal(err)
	}
	portsBody, _ := io.ReadAll(ports.Body)
	ports.Body.Close()
	if ports.StatusCode != 200 || !strings.Contains(string(portsBody), "ports") {
		t.Fatalf("ports %d %s", ports.StatusCode, portsBody)
	}

	res, err := http.PostForm(srv.URL+"/api/identify", url.Values{
		"port": {""}, "baud": {"115200"}, "reset": {"manual"},
	})
	if err != nil {
		t.Fatal(err)
	}
	msg, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(msg), "没有指定串口") {
		t.Fatalf("identify %d %s", res.StatusCode, msg)
	}
}
