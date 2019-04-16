package logger

import (
	"fmt"
	"os"
	"path"
	"regexp"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/gorilla/websocket"
)

// Logger 应用使用的 logger 实例
var Logger = logrus.New().WithField("name", "haruno")

// LogTypeInfo 信息类型
const LogTypeInfo = 0

// LogTypeError 错误类型
const LogTypeError = 1

// LogTypeSuccess 成功类型
const LogTypeSuccess = 2

// maxQueueSize 队列最大大小
// == 用户首次通过websocket链接能看到的最大的日志数量
const maxQueueSize = 10

var logTypeStr = []string{"info", "error", "success"}

// Log log消息格式(json)
type Log struct {
	Time int64  `json:"time"`
	Type int    `json:"type"`
	Text string `json:"text"`
}

// NewLog 创建一个新的Log实例
func NewLog(ltype int, text string) *Log {
	now := time.Now().Unix()
	return &Log{
		Time: now,
		Type: ltype,
		Text: text,
	}
}

// LogInterface 基础的logger接口
type LogInterface interface {
	Success(string)
	Successf(string, ...interface{})
	Info(string)
	Infof(string, ...interface{})
	Error(string)
	Errorf(string, ...interface{})
}

type loggerWithField struct {
	field   string
	service *loggerService
	LogInterface
}

// Success 成功log
func (logger *loggerWithField) Success(text string) {
	logger.service.Successf("%s: %s", logger.field, text)
}

// Success 格式化成功log
func (logger *loggerWithField) Successf(format string, args ...interface{}) {
	logger.service.Successf("%s: %s", logger.field, fmt.Sprintf(format, args...))
}

// Info 信息log
func (logger *loggerWithField) Info(text string) {
	logger.service.Infof("%s: %s", logger.field, text)
}

// Infof 格式化信息log
func (logger *loggerWithField) Infof(format string, args ...interface{}) {
	logger.service.Infof("%s: %s", logger.field, fmt.Sprintf(format, args...))
}

// Error 错误log
func (logger *loggerWithField) Error(text string) {
	logger.service.Errorf("%s: %s", logger.field, text)
}

// Errorf 格式化错误log
func (logger *loggerWithField) Errorf(format string, args ...interface{}) {
	logger.service.Errorf("%s: %s", logger.field, fmt.Sprintf(format, args...))
}

type loggerService struct {
	conns    map[*websocket.Conn]bool
	success  int
	fails    int
	logsPath string
	logChan  chan *Log
	logLT    string
	fpSI     *os.File
	fpE      *os.File
	logS     *logrus.Entry
	logI     *logrus.Entry
	logE     *logrus.Entry
	wscLock  sync.Mutex
	LogInterface
}

// 时间格式等基本的常量
const logDateFormat = "2006-01-02"
const pongWaitTime = 5 * time.Second

// Service 单例实体
var Service loggerService

// SetLogsPath 设置log文件目录
func (logger *loggerService) SetLogsPath(p string) {
	logger.logsPath = p
}

// LogsPath 获取logs文件的绝对路径
func (logger *loggerService) LogsPath() string {
	pwd, _ := os.Getwd()
	return path.Join(pwd, logger.logsPath)
}

// LogFile 获取当前log文件的位置
func (logger *loggerService) LogFile(scope string) string {
	date := time.Now().Format(logDateFormat)
	filename := fmt.Sprintf("%s.log", date)
	if len(scope) != 0 {
		filename = fmt.Sprintf("%s-%s.log", date, scope)
	}
	return path.Join(logger.LogsPath(), filename)
}

// Success 获取成功计数
func (logger *loggerService) SuccessCnt() int {
	return logger.success
}

// Success 获取失败计数
func (logger *loggerService) FailCnt() int {
	return logger.fails
}

func (logger *loggerService) sLogFiles() {
	var err error
	var newfp *os.File
	var oldfp *os.File
	logfileN := logger.LogFile("")
	if logfileN != logger.logLT {
		logger.logLT = logfileN

		oldfp = logger.fpSI
		newfp, err = os.OpenFile(logfileN, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			Logger.Fatalln(err)
		}
		if oldfp != nil {
			err = oldfp.Close()
			if err != nil {
				Logger.Fatalln(err)
			}
		}
		logger.logS.Logger.SetOutput(newfp)
		logger.logI.Logger.SetOutput(newfp)
		logger.fpSI = newfp

		oldfp = logger.fpE
		newfp, err = os.OpenFile(logger.LogFile("error"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			Logger.Fatalln(err)
		}
		if oldfp != nil {
			err = oldfp.Close()
			if err != nil {
				Logger.Fatalln(err)
			}
		}
		logger.logE.Logger.SetOutput(newfp)
		logger.fpE = newfp
	}
}

func escapeCRLF(s string) string {
	cr, _ := regexp.Compile(`\r`)
	lf, _ := regexp.Compile(`\n`)
	s = cr.ReplaceAllString(s, "\\r")
	s = lf.ReplaceAllString(s, "\\n")
	return s
}

func escapeHost(s string) string {
	host, _ := regexp.Compile(`(\d+)\.\d+\.\d+\.(\d+)(?:\:(\d+))?`)
	s = host.ReplaceAllString(s, "$1.*.*.$2:$3")
	return s
}

// Add 往队列里加入一个新的log
func (logger *loggerService) Add(lg *Log) {
	logger.sLogFiles()
	lg.Text = escapeHost(lg.Text)
	logMsg := escapeCRLF(lg.Text)
	switch lg.Type {
	case LogTypeSuccess:
		logger.success++
		Logger.WithField("type", "success").Println(logMsg)
		logger.logS.Println(lg.Text)
	case LogTypeError:
		logger.fails++
		Logger.WithField("type", "error").Errorln(logMsg)
		logger.logE.Println(lg.Text)
	default:
		Logger.WithField("type", "info").Println(logMsg)
		logger.logI.Println(lg.Text)
	}
	logger.logChan <- lg
	if len(logger.logChan) >= maxQueueSize {
		<-logger.logChan
	}
}

// AddLog 往队列里加入一个新的log
func (logger *loggerService) AddLog(ltype int, text string) {
	logger.Add(NewLog(ltype, text))
}

// Field 设置logger的域
func (logger *loggerService) Field(name string) LogInterface {
	return &loggerWithField{field: name, service: logger}
}

// Success 成功log
func (logger *loggerService) Success(text string) {
	logger.AddLog(LogTypeSuccess, text)
}

// Successf 格式化成功log
func (logger *loggerService) Successf(format string, args ...interface{}) {
	logger.AddLog(LogTypeSuccess, fmt.Sprintf(format, args...))
}

// Info 信息log
func (logger *loggerService) Info(text string) {
	logger.AddLog(LogTypeInfo, text)
}

// Infof 格式化信息log
func (logger *loggerService) Infof(format string, args ...interface{}) {
	logger.AddLog(LogTypeInfo, fmt.Sprintf(format, args...))
}

// Error 错误log
func (logger *loggerService) Error(text string) {
	logger.AddLog(LogTypeError, text)
}

// Errorf 格式化错误log
func (logger *loggerService) Errorf(format string, args ...interface{}) {
	logger.AddLog(LogTypeError, fmt.Sprintf(format, args...))
}

func (logger *loggerService) setConn(conn *websocket.Conn, state bool) {
	logger.wscLock.Lock()
	defer logger.wscLock.Unlock()
	logger.conns[conn] = state
}

func (logger *loggerService) delConn(conn *websocket.Conn) {
	logger.wscLock.Lock()
	defer logger.wscLock.Unlock()
	delete(logger.conns, conn)
}

func setupPong(conn *websocket.Conn, quit chan int) {
	pongTicker := time.NewTicker(pongWaitTime)
	pongMsg := []byte("")
	go func() {
		defer pongTicker.Stop()
		defer conn.Close()
		defer Service.delConn(conn)
		for {
			if Service.conns[conn] != true {
				close(quit)
			}
			select {
			case <-quit:
				return
			case <-pongTicker.C:
				conn.SetWriteDeadline(time.Now().Add(pongWaitTime))
				if err := conn.WriteMessage(websocket.PongMessage, pongMsg); err != nil {
					close(quit)
				}
			}
		}
	}()
}

// Initialize 初始化logger服务
func (logger *loggerService) Initialize() {
	// 建立日志目录
	if logger.logsPath == "" {
		Logger.Fatal("LogsPath not set please use logger.Default.SetLogsPath func set it.")
	}
	logspath := logger.LogsPath()
	Logger.Printf("LogsPath = %s", logspath)
	_, err := os.Stat(logspath)
	if err != nil {
		// 不存在目录的时候创建目录
		if os.IsNotExist(err) {
			Logger.Println("LogsPath is not existed.")
			err = os.Mkdir(logspath, 0700)
			if err != nil {
				Logger.Fatal("Logger", err)
			}
			Logger.Println("LogsPath created successfully.")
		}
	}
	// 创建连接池
	logger.conns = make(map[*websocket.Conn]bool)
	// 创建log管道
	logger.logChan = make(chan *Log, maxQueueSize)
	// 创建 logrus success 实例
	logger.logS = logrus.New().WithFields(logrus.Fields{
		"name": "haruno",
		"type": "success",
	})
	// 创建 logrus info 实例
	logger.logI = logrus.New().WithFields(logrus.Fields{
		"name": "haruno",
		"type": "info",
	})
	// 创建 logrus error 实例
	logger.logE = logrus.New().WithFields(logrus.Fields{
		"name": "haruno",
		"type": "error",
	})
	logger.logS.Logger.SetFormatter(&logrus.TextFormatter{})
	logger.logI.Logger.SetFormatter(&logrus.TextFormatter{})
	logger.logE.Logger.SetFormatter(&logrus.TextFormatter{})
	logger.sLogFiles()
}
