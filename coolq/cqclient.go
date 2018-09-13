package coolq

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/haruno-bot/haruno/clients"
	"github.com/haruno-bot/haruno/logger"
)

const timeForWait = 30

const noFilterKey = "__NEVER_SET_UNUSED_KEY__"

// Filter 过滤函数
type Filter func(*CQEvent) bool

// Handler 处理函数
type Handler func(*CQEvent)

type pluginEntry struct {
	fitlers  map[string]Filter
	handlers map[string]Handler
}

// cqclient 酷q机器人连接客户端
// 为了安全起见，暂时不允许在包外额外创建
type cqclient struct {
	mu            sync.Mutex
	apiConn       *clients.WSClient
	eventConn     *clients.WSClient
	pluginEntries map[string]pluginEntry
	echoqueue     map[int64]bool
}

func handleConnect(conn *clients.WSClient) {
	if conn.IsConnected() {
		msgText := fmt.Sprintf("%s服务已成功连接！", conn.Name)
		log.Println(msgText)
		logger.Service.AddLog(logger.LogTypeInfo, msgText)
	}
}

func handleError(err error) {
	msgText := err.Error()
	errMsg := logger.NewLog(logger.LogTypeError, msgText)
	logger.Service.Add(errMsg)
}

func (c *cqclient) registerAllPlugins() {
	// 先全部执行加载函数
	for _, plug := range entries {
		err := plug.Load()
		if err != nil {
			log.Fatalln(err.Error())
		}
	}
	// 注册所有的handler和filter
	for _, plug := range entries {
		pluginName := plug.Name()
		pluginFilters := plug.Filters()
		pluginHandlers := plug.Handlers()
		hasFilter := make(map[string]bool)
		entry := pluginEntry{
			fitlers:  make(map[string]Filter),
			handlers: make(map[string]Handler),
		}
		noFilterHanlers := make([]Handler, 0)
		for key, filter := range pluginFilters {
			handler := pluginHandlers[key]
			if handler == nil {
				fmt.Printf("[WARN] 插件 %s 中存在没有使用的key: %s\n", pluginName, key)
				continue
			}
			hasFilter[key] = true
			entry.fitlers[key] = filter
			entry.handlers[key] = handler
		}
		for key, handler := range pluginHandlers {
			if !hasFilter[key] {
				noFilterHanlers = append(noFilterHanlers, handler)
			}
		}
		entry.handlers[noFilterKey] = func(event *CQEvent) {
			for _, hanldeFunc := range noFilterHanlers {
				hanldeFunc(event)
			}
		}
		c.pluginEntries[pluginName] = entry
	}
	// 触发所有插件的onload事件
	for _, plug := range entries {
		go plug.Loaded()
	}
}

func (c *cqclient) Initialize() {
	c.apiConn.Name = "酷Q机器人Api"
	c.eventConn.Name = "酷Q机器人Event"
	c.registerAllPlugins()
	// handle connect
	c.apiConn.OnConnect = handleConnect
	c.eventConn.OnConnect = handleConnect
	// handle error
	c.apiConn.OnError = handleError
	c.eventConn.OnError = handleError
	// handle message
	c.apiConn.OnMessage = func(raw []byte) {
		msg := new(CQWSResponse)
		err := json.Unmarshal(raw, msg)
		if err != nil {
			logger.Service.AddLog(logger.LogTypeError, err.Error())
			return
		}
		echo := msg.Echo
		if c.echoqueue[echo] {
			c.mu.Lock()
			delete(c.echoqueue, echo)
			c.mu.Unlock()
		}
	}
	// handle events
	c.eventConn.OnMessage = func(raw []byte) {
		event := new(CQEvent)
		err := json.Unmarshal(raw, event)
		if err != nil {
			errMsg := err.Error()
			log.Panicln(errMsg)
			logger.Service.AddLog(logger.LogTypeError, errMsg)
			return
		}
		for _, entry := range c.pluginEntries {
			entry.handlers[noFilterKey](event)
			for key, filterFunc := range entry.fitlers {
				handleFunc := entry.handlers[key]
				if filterFunc(event) {
					handleFunc(event)
				}
			}
		}
	}

	// 定时清理echo序列
	go func() {
		ticker := time.NewTicker(timeForWait * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				now := time.Now().Unix()
				for echo, state := range c.echoqueue {
					if state && now-echo > timeForWait {
						logger.Service.AddLog(logger.LogTypeError, fmt.Sprintf("Echo = %d 响应超时(30s).", echo))
						c.mu.Lock()
						delete(c.echoqueue, echo)
						c.mu.Unlock()
					}
				}
			}
		}
	}()
}

// Connect 连接远程酷q api服务
// url 形如 ws://127.0.0.1:8080, wss://127.0.0.1:8080之类的url
// token 酷q机器人的access token
func (c *cqclient) Connect(url string, token string) {
	headers := make(http.Header)
	headers.Add("Authorization", fmt.Sprintf("Token %s", token))
	// 连接api服务和事件服务
	c.apiConn.Dial(fmt.Sprintf("%s/api", url), headers)
	c.eventConn.Dial(fmt.Sprintf("%s/event", url), headers)
}

// IsAPIOk api服务是否可用
func (c *cqclient) IsAPIOk() bool {
	return c.apiConn.IsConnected()
}

// IsEventOk event服务是否可用
func (c *cqclient) IsEventOk() bool {
	return c.eventConn.IsConnected()
}

func (c *cqclient) SendGroupMsg(groupID int64, message string) {
	if !c.IsAPIOk() {
		return
	}
	payload := &CQWSMessage{
		Action: ActionSendGroupMsg,
		Params: CQTypeSendGroupMsg{
			GroupID: groupID,
			Message: message,
		},
		Echo: time.Now().Unix(),
	}
	msg, _ := json.Marshal(payload)
	c.apiConn.Send(websocket.TextMessage, msg)
}

// Client 唯一的酷q机器人实体
var Client = &cqclient{
	apiConn:       new(clients.WSClient),
	eventConn:     new(clients.WSClient),
	pluginEntries: make(map[string]pluginEntry),
}
