// Package gui serves the local programming page and the serial operations behind it.
package gui

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"strconv"
	"sync"
	"time"

	"stm8s105-flasher/internal/boot"
	"stm8s105-flasher/internal/image"
	"stm8s105-flasher/internal/port"
)

//go:embed web/index.html
var page embed.FS

// Serve listens on 127.0.0.1 and opens the page in a browser.
func Serve() error {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	url := "http://" + ln.Addr().String()
	fmt.Println("STM8S105 串口烧录界面：", url)
	go openBrowser(url)
	return http.Serve(ln, handler())
}

func handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		f, err := page.Open("web/index.html")
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		defer f.Close()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.Copy(w, f)
	})
	app := &server{ops: make(chan struct{}, 1)}
	mux.HandleFunc("/api/ports", app.ports)
	mux.HandleFunc("/api/identify", app.identify)
	mux.HandleFunc("/api/erase", app.erase)
	mux.HandleFunc("/api/write", app.write)
	mux.HandleFunc("/api/read", app.read)
	return mux
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}

type server struct {
	mu  sync.Mutex
	ops chan struct{}
}

func (s *server) ports(w http.ResponseWriter, r *http.Request) {
	names, err := port.List()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, map[string]any{"ports": names})
}

func (s *server) identify(w http.ResponseWriter, r *http.Request) {
	s.stream(w, r, func(st *stream, req *http.Request) error {
		sess, p, err := s.connect(st, req)
		if err != nil {
			return err
		}
		defer p.Close()
		_, model, err := boot.Prepare(sess)
		if err != nil {
			return err
		}
		st.done("芯片是 " + model.Title)
		return nil
	})
}

func (s *server) erase(w http.ResponseWriter, r *http.Request) {
	s.stream(w, r, func(st *stream, req *http.Request) error {
		sess, p, err := s.connect(st, req)
		if err != nil {
			return err
		}
		defer p.Close()
		_, model, err := boot.Prepare(sess)
		if err != nil {
			return err
		}
		if err := boot.EraseFlash(sess, *model, formBool(req, "eeprom")); err != nil {
			return err
		}
		st.progress(1)
		st.done("擦除完成")
		return nil
	})
}

func (s *server) write(w http.ResponseWriter, r *http.Request) {
	s.stream(w, r, func(st *stream, req *http.Request) error {
		file, hdr, err := req.FormFile("file")
		if err != nil {
			return fmt.Errorf("没有上传固件")
		}
		defer file.Close()
		content, err := io.ReadAll(file)
		if err != nil {
			return err
		}
		img, err := image.Parse(hdr.Filename, content, 0x8000)
		if err != nil {
			return err
		}
		lo, hi, _ := img.Range()
		st.log(fmt.Sprintf("固件 %s，%d 字节，0x%04X–0x%04X", hdr.Filename, img.Len(), lo, hi))
		sess, p, err := s.connect(st, req)
		if err != nil {
			return err
		}
		defer p.Close()
		_, model, err := boot.Prepare(sess)
		if err != nil {
			return err
		}
		if err := boot.Program(sess, *model, img, formBool(req, "keep"), formBool(req, "verify"), formBool(req, "eeprom"), st.progress); err != nil {
			return err
		}
		if formBool(req, "run") {
			if err := sess.Go(model.FlashStart); err != nil {
				return err
			}
		}
		st.done("烧录完成")
		return nil
	})
}

func (s *server) read(w http.ResponseWriter, r *http.Request) {
	s.stream(w, r, func(st *stream, req *http.Request) error {
		sess, p, err := s.connect(st, req)
		if err != nil {
			return err
		}
		defer p.Close()
		_, model, err := boot.Prepare(sess)
		if err != nil {
			return err
		}
		img, err := boot.ReadFlash(sess, *model, st.progress)
		if err != nil {
			return err
		}
		st.event(map[string]any{
			"hex":  string(img.IntelHex()),
			"name": model.ID + ".hex",
			"log":  fmt.Sprintf("已读回 %d 字节", img.Len()),
		})
		st.done("读回完成")
		return nil
	})
}

func (s *server) connect(st *stream, r *http.Request) (*boot.Session, port.Port, error) {
	name := r.FormValue("port")
	baud, _ := strconv.Atoi(r.FormValue("baud"))
	if baud == 0 {
		baud = 115200
	}
	reset := r.FormValue("reset")
	window := 10 * time.Second
	if reset == "dtr" || reset == "rts" {
		window = 3 * time.Second
	}
	st.log(fmt.Sprintf("打开 %s，%d 8N1", name, baud))
	return boot.Connect(name, baud, reset, window, st.log)
}

func (s *server) stream(w http.ResponseWriter, r *http.Request, fn func(*stream, *http.Request) error) {
	if err := r.ParseMultipartForm(32 << 20); err != nil && r.Method == http.MethodPost {
		// Identify and erase use urlencoded or multipart without a file.
		_ = r.ParseForm()
	}
	select {
	case s.ops <- struct{}{}:
		defer func() { <-s.ops }()
	default:
		http.Error(w, "已有烧录任务在进行", http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	fl, _ := w.(http.Flusher)
	st := &stream{w: w, fl: fl}
	if err := fn(st, r); err != nil {
		st.event(map[string]any{"error": err.Error()})
	}
}

type stream struct {
	mu sync.Mutex
	w  http.ResponseWriter
	fl http.Flusher
}

func (s *stream) log(line string) { s.event(map[string]any{"log": line}) }

func (s *stream) progress(v float64) { s.event(map[string]any{"progress": v}) }

func (s *stream) done(line string) { s.event(map[string]any{"done": line, "progress": 1}) }

func (s *stream) event(v map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	enc := json.NewEncoder(s.w)
	_ = enc.Encode(v)
	if s.fl != nil {
		s.fl.Flush()
	}
}

func formBool(r *http.Request, key string) bool {
	v := r.FormValue(key)
	return v == "1" || v == "true" || v == "on"
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(v)
}
