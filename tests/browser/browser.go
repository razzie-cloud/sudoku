// Package browser runs disposable Chromium fixtures through its DevTools protocol.
// It needs a Chromium executable, but no Playwright, Node, or downloaded Go tools.
package browser

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/websocket"
)

type Request struct {
	URL, Method, PostData string
	Headers               map[string]string
}
type Response struct {
	Body    []byte
	Type    string
	Status  int
	Headers map[string]string
}
type message struct {
	ID     int64           `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  json.RawMessage `json:"error"`
}
type Browser struct {
	conn    *websocket.Conn
	cmd     *exec.Cmd
	cancel  context.CancelFunc
	profile string
	mu      sync.Mutex
	pending map[int64]chan message
	next    atomic.Int64
	errors  []string
	dialogs atomic.Int32
	closed  chan struct{}
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}
func Start(route func(Request) Response) *Browser {
	binary := os.Getenv("PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH")
	if binary == "" {
		binary = os.Getenv("CHROMIUM_EXECUTABLE_PATH")
	}
	if binary == "" {
		for _, name := range []string{"chromium", "chromium-browser", "google-chrome"} {
			if path, e := exec.LookPath(name); e == nil {
				binary = path
				break
			}
		}
	}
	if binary == "" {
		panic("set CHROMIUM_EXECUTABLE_PATH to an installed Chromium browser")
	}
	profile, e := os.MkdirTemp("", "sudoku-browser-*")
	check(e)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	b := &Browser{profile: profile, cancel: cancel, pending: map[int64]chan message{}, closed: make(chan struct{})}
	b.cmd = exec.CommandContext(ctx, binary, "--headless", "--no-sandbox", "--disable-gpu", "--no-first-run", "--no-default-browser-check", "--remote-debugging-port=0", "--remote-debugging-address=127.0.0.1", "--user-data-dir="+profile, "--autoplay-policy=no-user-gesture-required", "about:blank")
	var stderr bytes.Buffer
	b.cmd.Stderr = &stderr
	if e = b.cmd.Start(); e != nil {
		b.Close()
		panic(e)
	}
	// Every startup failure must also reap Chromium and remove its temporary profile.
	started := false
	defer func() {
		if !started {
			b.Close()
			if stderr.Len() > 0 {
				fmt.Fprintf(os.Stderr, "Chromium startup stderr:\n%s\n", stderr.String())
			}
		}
	}()
	deadline := time.Now().Add(10 * time.Second)
	port := waitDevToolsPort(filepath.Join(profile, "DevToolsActivePort"), deadline)
	if port == "" {
		panic("Chromium did not start; check CHROMIUM_EXECUTABLE_PATH")
	}
	client := &http.Client{Timeout: 5 * time.Second}
	var endpoint string
	for time.Now().Before(deadline) {
		res, err := client.Get("http://127.0.0.1:" + port + "/json/list")
		if err != nil {
			time.Sleep(25 * time.Millisecond)
			continue
		}
		var targets []struct {
			URL  string `json:"webSocketDebuggerUrl"`
			Type string `json:"type"`
		}
		err = json.NewDecoder(res.Body).Decode(&targets)
		res.Body.Close()
		check(err)
		for _, target := range targets {
			if target.Type == "page" {
				endpoint = target.URL
				break
			}
		}
		if endpoint != "" {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if endpoint == "" {
		panic("Chromium has no page target")
	}
	b.conn, e = websocket.Dial(endpoint, "", "http://127.0.0.1:"+port)
	check(e)
	b.conn.MaxPayloadBytes = 16 << 20
	go b.receive(route)
	b.Call("Runtime.enable", nil)
	b.Call("Page.enable", nil)
	b.Call("Fetch.enable", map[string]any{"patterns": []any{map[string]any{"urlPattern": "*"}}})
	started = true
	return b
}
func waitDevToolsPort(path string, deadline time.Time) string {
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(path)
		if err == nil {
			// Chromium creates the file before it finishes writing the port line.
			if port, _, complete := strings.Cut(string(raw), "\n"); complete && port != "" {
				return port
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	return ""
}
func (b *Browser) receive(route func(Request) Response) {
	defer close(b.closed)
	for {
		var m message
		if e := websocket.JSON.Receive(b.conn, &m); e != nil {
			return
		}
		if m.ID != 0 {
			b.mu.Lock()
			ch := b.pending[m.ID]
			b.mu.Unlock()
			if ch != nil {
				ch <- m
			}
			continue
		}
		if m.Method == "Page.javascriptDialogOpening" {
			b.dialogs.Add(1)
			go b.Call("Page.handleJavaScriptDialog", map[string]any{"accept": false})
		}
		if m.Method == "Runtime.exceptionThrown" {
			b.mu.Lock()
			b.errors = append(b.errors, string(m.Params))
			b.mu.Unlock()
		}
		if m.Method == "Fetch.requestPaused" {
			go func(raw json.RawMessage) {
				defer func() {
					if r := recover(); r != nil {
						// Tests deliberately abort delayed requests when another
						// navigation wins. Their interception may already be gone.
						if strings.Contains(fmt.Sprint(r), "Fetch.fulfillRequest:") && strings.Contains(fmt.Sprint(r), "Invalid InterceptionId") {
							return
						}
						b.mu.Lock()
						b.errors = append(b.errors, fmt.Sprint(r))
						b.mu.Unlock()
					}
				}()
				var p struct {
					ID      string  `json:"requestId"`
					Request Request `json:"request"`
				}
				check(json.Unmarshal(raw, &p))
				if route == nil {
					b.Call("Fetch.continueRequest", map[string]any{"requestId": p.ID})
					return
				}
				response := route(p.Request)
				if response.Status == 0 {
					response.Status = 200
				}
				headers := make([]any, 0)
				if response.Type != "" {
					headers = append(headers, map[string]string{"name": "Content-Type", "value": response.Type})
				}
				for k, v := range response.Headers {
					headers = append(headers, map[string]string{"name": k, "value": v})
				}
				b.Call("Fetch.fulfillRequest", map[string]any{"requestId": p.ID, "responseCode": response.Status, "responseHeaders": headers, "body": base64.StdEncoding.EncodeToString(response.Body)})
			}(m.Params)
		}
	}
}
func (b *Browser) Call(method string, params any) json.RawMessage {
	id := b.next.Add(1)
	ch := make(chan message, 1)
	b.mu.Lock()
	b.pending[id] = ch
	b.mu.Unlock()
	defer func() { b.mu.Lock(); delete(b.pending, id); b.mu.Unlock() }()
	check(websocket.JSON.Send(b.conn, map[string]any{"id": id, "method": method, "params": params}))
	select {
	case m := <-ch:
		if len(m.Error) > 0 {
			panic(fmt.Sprintf("%s: %s", method, m.Error))
		}
		return m.Result
	case <-b.closed:
		panic("Chromium connection closed")
	case <-time.After(12 * time.Second):
		panic("Chromium command timed out: " + method)
	}
}
func (b *Browser) Eval(js string) json.RawMessage {
	raw := b.Call("Runtime.evaluate", map[string]any{"expression": js, "awaitPromise": true, "returnByValue": true, "userGesture": true})
	var r struct {
		Result struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		Exception json.RawMessage `json:"exceptionDetails"`
	}
	check(json.Unmarshal(raw, &r))
	if len(r.Exception) > 0 {
		panic(fmt.Sprintf("%s\nExpression: %s", r.Exception, js))
	}
	return r.Result.Value
}
func (b *Browser) Run(js string) { b.Eval(`(async()=>{` + Helpers + js + `})()`) }
func (b *Browser) WaitFor(expression string) {
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		if b.matches(expression) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	panic("browser condition timed out: " + expression)
}

func (b *Browser) matches(expression string) (matched bool) {
	defer func() {
		if err := recover(); err != nil {
			message := fmt.Sprint(err)
			if !strings.Contains(message, "Inspected target navigated or closed") &&
				!strings.Contains(message, "Cannot find context with specified id") &&
				!strings.Contains(message, "Execution context was destroyed") {
				panic(err)
			}
		}
	}()
	return string(b.Eval("Boolean("+expression+")")) == "true"
}
func (b *Browser) Viewport(width, height int) {
	b.Call("Emulation.setDeviceMetricsOverride", map[string]any{"width": width, "height": height, "deviceScaleFactor": 1, "mobile": false})
}
func (b *Browser) Navigate(url string) {
	var navigation struct {
		LoaderID string `json:"loaderId"`
	}
	check(json.Unmarshal(b.Call("Page.navigate", map[string]any{"url": url}), &navigation))
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var tree struct {
			FrameTree struct {
				Frame struct {
					LoaderID string `json:"loaderId"`
				} `json:"frame"`
			} `json:"frameTree"`
		}
		check(json.Unmarshal(b.Call("Page.getFrameTree", nil), &tree))
		// location can change while the previous document still reports
		// complete. Wait for the requested document to commit first.
		if (navigation.LoaderID == "" || tree.FrameTree.Frame.LoaderID == navigation.LoaderID) && string(b.Eval(`location.href===`+Quote(url)+`&&document.readyState==='complete'`)) == "true" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	panic("page navigation timed out")
}
func (b *Browser) DialogCount() int32 { return b.dialogs.Load() }
func (b *Browser) AssertNoErrors() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.errors) > 0 {
		panic(strings.Join(b.errors, "\n"))
	}
}
func (b *Browser) Close() {
	if b.conn != nil {
		b.conn.Close()
	}
	if b.cancel != nil {
		b.cancel()
	}
	if b.cmd != nil && b.cmd.Process != nil {
		_ = b.cmd.Wait()
	}
	if b.profile != "" {
		os.RemoveAll(b.profile)
	}
}
func Quote(v string) string { b, e := json.Marshal(v); check(e); return string(b) }

// Keep assertions inside the real browser; wait on observable behavior rather than sleeps.
const Helpers = `
const assert=(ok,message)=>{if(!ok)throw Error(message||'assertion failed')};
const wait=async(fn)=>{const end=Date.now()+5000;while(!fn()){if(Date.now()>end)throw Error('timed out: '+fn);await new Promise(r=>setTimeout(r,20));}};
const el=s=>{const e=document.querySelector(s);assert(e,'missing '+s);return e};
const settled=()=>wait(()=>document.querySelector('#workspace:not(.htmx-settling)'));
const frame=()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r)));
`
