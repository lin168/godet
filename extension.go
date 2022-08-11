package godet

import (
	"encoding/json"
	"fmt"
	"github.com/gobs/httpclient"
	"github.com/gorilla/websocket"
	"log"
	"regexp"
	"sync"
)

// EventCallback represents a callback event, associated with a method.
type EventCallback func(params Params)

//var callbacks map[string]EventCallback
var callbacks sync.Map

var eventChan chan wsMessage
var client *httpclient.HttpClient
var remoteDebuggerList []*RemoteDebugger

// RemoteDebugger implements an interface for Chrome DevTools.
type RemoteDebugger struct {
	sync.Mutex
	http      *httpclient.HttpClient
	wsConn    *websocket.Conn // websocket connection
	current   string
	reqID     int
	verbose   bool
	closed    chan bool
	wg        *sync.WaitGroup
	requests  chan Params
	responses map[int]chan json.RawMessage
}

// TargetEvents enables Target events listening.
// Adding by aiwen
func (remote *RemoteDebugger) TargetEvents(enable bool) error {
	param := make(Params)
	param["discover"] = enable

	_, err := remote.SendRequest("Target.setDiscoverTargets", param)
	return err
}

// TargetId return the target ID associate with the Connection
func (remote *RemoteDebugger) TargetId() string {
	return remote.current
}

func (remote *RemoteDebugger) GetTitle(requestId string) string {
	param := Params{
		"requestId": requestId,
	}

	resp, err := remote.SendRequest("Network.getResponseBody", param)
	if err != nil || resp == nil {
		return ""
	}

	if body, ok := resp["body"]; ok {
		var re = regexp.MustCompile(`(?m)<title>(.*)</title>`)
		matches := re.FindStringSubmatch(body.(string))
		if len(matches) == 1 {
			return ""
		}
		return matches[1]
	}

	return ""
}

// 处理收到的事件
func processEvents() {
	for ev := range eventChan {
		load, ok := callbacks.Load(ev.Method)
		if !ok {
			continue
		}
		cb := load.(EventCallback)

		//cb := callbacks[ev.Method]

		if cb != nil {
			var params Params
			if err := json.Unmarshal(ev.Params, &params); err != nil {
				log.Println("unmarshal", string(ev.Params), len(ev.Params), err)
			} else {
				cb(params)
			}
		}
	}
}

func (remote *RemoteDebugger) connectWs(tab *Tab) error {
	if tab == nil || len(tab.WsURL) == 0 {
		tabs, err := remote.TabList("page")
		if err != nil {
			return err
		}

		if len(tabs) == 0 {
			return ErrorNoActiveTab
		}

		if tab == nil {
			tab = tabs[0]
		} else {
			for _, t := range tabs {
				if tab.ID == t.ID {
					tab.WsURL = t.WsURL
					break
				}
			}
		}
	}

	if remote.wsConn != nil {
		if tab.ID == remote.current {
			// nothing to do
			return nil
		}

		if remote.verbose {
			log.Println("disconnecting from current tab, id", remote.current)
		}

		remote.Lock()
		ws := remote.wsConn
		remote.wsConn, remote.current = nil, ""
		remote.Unlock()

		_ = ws.Close()
	}

	if len(tab.WsURL) == 0 {
		return ErrorNoWsURL
	}

	// check websocket connection
	if remote.verbose {
		log.Println("connecting to tab", tab.WsURL)
	}

	d := &websocket.Dialer{
		ReadBufferSize:  MaxReadBufferSize,
		WriteBufferSize: MaxWriteBufferSize,
	}

	ws, _, err := d.Dial(tab.WsURL, nil)
	if err != nil {
		if remote.verbose {
			log.Println("dial error:", err)
		}
		return err
	}

	remote.Lock()
	remote.wsConn = ws
	remote.current = tab.ID
	remote.Unlock()

	go remote.sendMessages()
	go remote.readMessages(ws)
	remote.wg.Add(2)

	_ = remote.PageEvents(true)
	_ = remote.NetworkEvents(true)
	_ = remote.TargetEvents(true)
	_ = remote.SetCacheDisabled(true)

	return nil
}

// StartCapture 连接当前活跃tab页，并开始接收消息
func StartCapture(port string, verbose bool, options ...ConnectOption) (*RemoteDebugger, error) {
	//callbacks = make(map[string]EventCallback)
	eventChan = make(chan wsMessage, 1024)

	// 创建一个到Chrome调试端口的HTTP连接，使用/json系列api
	client = httpclient.NewHttpClient("http://" + port)
	for _, setOption := range options {
		setOption(client)
	}

	remote := &RemoteDebugger{
		http:      client,
		requests:  make(chan Params, 1),
		responses: map[int]chan json.RawMessage{},
		closed:    make(chan bool),
		verbose:   verbose,
		wg:        &sync.WaitGroup{},
	}

	// remote.http.Verbose = verbose
	if verbose {
		httpclient.StartLogging(false, true, false)
	}
	// 连接当前活跃标签页，初始只有一个标签页
	if err := remote.connectWs(nil); err != nil {
		return nil, err
	}

	// 添加到Client列表
	remoteDebuggerList = append(remoteDebuggerList, remote)

	go processEvents()
	return remote, nil
}

// StopCapture stop service
func StopCapture() {
	fmt.Println(len(remoteDebuggerList))
	for _, remote := range remoteDebuggerList {
		_ = remote.Close()
	}

	close(eventChan)
}

// AddEventListener add event handler to the service
func AddEventListener(method string, cb EventCallback) {
	//callbacks[method] = cb
	callbacks.Store(method, cb)

}

func AddDebugger(targetId string, baseWsUrl string) error {
	// param check
	if len(baseWsUrl) == 0 || len(targetId) == 0 {
		return ErrorNoWsURL
	}

	// 创建RemoteDebugger Client
	remote := &RemoteDebugger{
		http:      client,
		requests:  make(chan Params, 1),
		responses: map[int]chan json.RawMessage{},
		closed:    make(chan bool),
		verbose:   false,
		wg:        &sync.WaitGroup{},
	}

	// 添加到Client列表
	remoteDebuggerList = append(remoteDebuggerList, remote)

	d := &websocket.Dialer{
		ReadBufferSize:  MaxReadBufferSize,
		WriteBufferSize: MaxWriteBufferSize,
	}

	ws, _, err := d.Dial(baseWsUrl+targetId, nil)
	if err != nil {
		if remote.verbose {
			log.Println("dial error:", err)
		}
		return err
	}

	remote.Lock()
	remote.wsConn = ws
	remote.current = targetId
	remote.Unlock()

	// 启动接收协程
	go remote.sendMessages()
	go remote.readMessages(ws)

	remote.wg.Add(2)

	// 开始接收事件
	_ = remote.PageEvents(true)
	_ = remote.NetworkEvents(true)
	_ = remote.TargetEvents(true)
	_ = remote.SetCacheDisabled(true)

	return nil
}

// Close the RemoteDebugger connection.
func (remote *RemoteDebugger) Close() (err error) {
	remote.Lock()
	ws := remote.wsConn
	remote.wsConn = nil

	if ws != nil { // already closed
		err = ws.Close()
		close(remote.closed)
		close(remote.requests)
	}
	remote.Unlock()

	// 等待发送和接收协程退出
	remote.wg.Wait()

	if remote.verbose {
		httpclient.StopLogging()
	}

	return
}

//------------------------------------------------------------------------------------------//

// Params is a type alias for the event params structure.
type Params map[string]interface{}

func (p Params) String(k string) string {
	if p == nil {
		return ""
	}

	if i, ok := p[k]; ok {
		if val, ok := i.(string); ok {
			return val
		}
	}

	return ""
}

func (p Params) Int(k string) int {
	if p == nil {
		return 0
	}

	if i, ok := p[k]; ok {
		if val, ok := i.(int); ok {
			return val
		}
	}

	return 0
}

func (p Params) Bool(k string) bool {
	if p == nil {
		return false
	}

	if i, ok := p[k]; ok {
		if val, ok := i.(bool); ok {
			return val
		}
	}

	return false
}

func (p Params) Map(k string) map[string]interface{} {
	if p == nil {
		return nil
	}

	if i, ok := p[k]; ok {
		if val, ok := i.(map[string]interface{}); ok {
			return val
		}
	}

	return nil
}

// Float64 return the float value
func (p Params) Float64(k string) float64 {
	if p == nil {
		return 0
	}

	if i, ok := p[k]; ok {
		if val, ok := i.(float64); ok {
			return val
		}
	}

	return 0
}
